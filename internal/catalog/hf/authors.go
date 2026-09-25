package hf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// AuthorRepo is one repo the Hub's author listing named.
type AuthorRepo struct {
	ID        string `json:"id"`        // "owner/name"
	CreatedAt string `json:"createdAt"` // RFC 3339; "" when the Hub did not send one
	Private   bool   `json:"private"`
	Gated     Gated  `json:"gated"`
	Disabled  bool   `json:"disabled"`
}

// AuthorListing is one answer to ListByAuthor.
type AuthorListing struct {
	Repos []AuthorRepo
	ETag  string
	// NotModified is true when the Hub answered 304 to the given etag —
	// nothing has changed under this author since the last check.
	NotModified bool
}

// ListByAuthor lists author's repos on the Hub, newest first — the new-
// model watch's only use for this: the curator's maintainers (a curated
// size's hf_base_repo owner) sometimes publish a repo the catalogue does
// not know yet, and that is flagged for review, never recommended
// (build-plan step 10, ARCHITECTURE.md D-57). limit bounds how many repos
// come back (the Hub's default page is small; this asks for exactly
// limit). etag, when non-empty, asks for a 304 if nothing has changed.
//
// Unlike ModelInfo, this is not cached across restarts (no cached body to
// reuse on a 304): the watch keeps only the etag, in watch_state, and a
// 304 here simply means "read again next time" — the daemon calls this at
// most once a day per maintainer, so the saving is not worth the extra
// state.
func (c *Client) ListByAuthor(ctx context.Context, author, etag string) (*AuthorListing, error) {
	author = strings.TrimSpace(author)
	if author == "" {
		return nil, fmt.Errorf("hf: ListByAuthor: empty author")
	}
	u := strings.TrimRight(c.BaseURL, "/") + "/api/models?" + url.Values{
		"author":    {author},
		"sort":      {"createdAt"},
		"direction": {"-1"},
		"full":      {"false"},
		"limit":     {"200"},
	}.Encode()
	resp, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out := &AuthorListing{ETag: resp.Header.Get("ETag")}
	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		c.count(func(s *Stats) { s.BytesRead += int64(len(body)) })
		if err != nil {
			return nil, fmt.Errorf("hf: reading %s's repos: %w", author, err)
		}
		if err := json.Unmarshal(body, &out.Repos); err != nil {
			return nil, fmt.Errorf("hf: %s's repo listing is not the JSON expected: %w", author, err)
		}
	case http.StatusNotModified:
		c.count(func(s *Stats) { s.NotModified++ })
		out.NotModified = true
		if out.ETag == "" {
			out.ETag = etag
		}
	default:
		return nil, statusError(resp)
	}
	return out, nil
}
