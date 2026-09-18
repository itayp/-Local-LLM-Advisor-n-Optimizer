// Package hf is the Hugging Face Hub client the catalogue refresh uses. It
// does two things and nothing else:
//
//   - ModelInfo: a repo's file list with sizes and content hashes
//     (GET /api/models/{repo}?blobs=true), sent with If-None-Match so an
//     unchanged repo costs a 304;
//   - ReadHeader: a GGUF file's header, read with HTTP range requests from
//     the file's first byte and parsed as it arrives (internal/catalog/gguf),
//     so the transfer stops where the parse stops — a few kilobytes of a
//     multi-gigabyte file. It never downloads weights: a server that ignores
//     the Range header is refused, not read.
//
// It is polite by construction: one request at a time with a minimum gap,
// Retry-After and Hugging Face's RateLimit header honoured on 429/503, a
// bounded number of retries, and redirects followed only to Hugging Face's
// own hosts. Nothing about the user or the machine is sent; the User-Agent
// names the product and its version.
package hf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"advisor/internal/catalog/gguf"
)

// DefaultBaseURL is the Hub. The only host this package talks to, with its
// CDN subdomains (see allowedHost).
const DefaultBaseURL = "https://huggingface.co"

// Client talks to the Hub. The zero value is not usable; call New.
type Client struct {
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
	Log       *slog.Logger

	// MinInterval is the smallest gap between two requests. The Hub's
	// published limits are per five minutes and far above what a refresh
	// needs; the gap keeps a refresh from ever bursting.
	MinInterval time.Duration
	// MaxWait is the longest a single Retry-After is honoured. A longer one
	// fails the request with ErrRateLimited instead of stalling the refresh.
	MaxWait time.Duration
	// MaxAttempts bounds retries of 429, 5xx and network errors.
	MaxAttempts int

	// FirstChunk and MaxChunk size the range requests: the first asks for
	// FirstChunk bytes, each further one twice the last, up to MaxChunk.
	FirstChunk int64
	MaxChunk   int64

	// Sleep waits for d or until ctx is done. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error

	mu   sync.Mutex
	last time.Time

	statsMu sync.Mutex
	stats   Stats
}

// Stats counts what the client did, for the refresh report.
type Stats struct {
	Requests    int   // HTTP requests sent, retries and redirects' first hops included
	NotModified int   // model-info answered 304
	Retries     int   // requests repeated after a 429, 5xx or network error
	BytesRead   int64 // response body bytes read: listings + header ranges
}

// New returns a client for the Hub with the defaults a refresh uses.
func New(userAgent string) *Client {
	c := &Client{
		BaseURL:     DefaultBaseURL,
		UserAgent:   userAgent,
		Log:         slog.Default(),
		MinInterval: 200 * time.Millisecond,
		MaxWait:     2 * time.Minute,
		MaxAttempts: 4,
		FirstChunk:  64 << 10,
		MaxChunk:    8 << 20,
		Sleep:       sleepCtx,
	}
	c.HTTP = &http.Client{
		Timeout:       2 * time.Minute,
		CheckRedirect: c.checkRedirect,
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Stats returns a snapshot of the counters.
func (c *Client) Stats() Stats {
	c.statsMu.Lock()
	defer c.statsMu.Unlock()
	return c.stats
}

func (c *Client) count(f func(*Stats)) {
	c.statsMu.Lock()
	f(&c.stats)
	c.statsMu.Unlock()
}

// Errors a caller can act on.
var (
	// ErrNotFound: no such repo, revision or file.
	ErrNotFound = errors.New("hf: not found")
	// ErrGated: the repo needs a Hugging Face login (gated or private). The
	// catalogue must name an ungated repo; this client never sends a token.
	ErrGated = errors.New("hf: the repository is gated or private")
	// ErrRateLimited: the Hub kept answering 429 (or asked for a longer wait
	// than MaxWait).
	ErrRateLimited = errors.New("hf: rate limited by Hugging Face")
	// ErrRangeIgnored: the server answered a range request with the whole
	// file. The request is abandoned rather than download weights.
	ErrRangeIgnored = errors.New("hf: the server ignored the range request; refusing to download the whole file")
	// ErrRedirect: a redirect pointed away from Hugging Face's hosts.
	ErrRedirect = errors.New("hf: redirect to a host outside Hugging Face refused")
	// ErrUnreachable: every attempt failed below HTTP (no network, DNS, a
	// proxy or firewall refusing the connection). A refresh stops at the
	// first one rather than failing every size the same way.
	ErrUnreachable = errors.New("hf: Hugging Face could not be reached")
)

// StatusError is an HTTP answer the client could not use.
type StatusError struct {
	Status int
	URL    string
	Body   string // the first bytes of the body, for the log
	err    error  // one of the sentinel errors above, when one applies
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("hf: %s answered HTTP %d", e.URL, e.Status)
	if e.err != nil {
		msg = e.err.Error() + " (" + msg + ")"
	}
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

func (e *StatusError) Unwrap() error { return e.err }

// allowedHost admits the Hub and its CDNs (cdn-lfs.huggingface.co,
// cas-bridge.xethub.hf.co, ...) and the host of BaseURL itself (tests).
func (c *Client) allowedHost(host string) bool {
	host = strings.ToLower(host)
	if base, err := url.Parse(c.BaseURL); err == nil && strings.EqualFold(base.Host, host) {
		return true
	}
	h := host
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	for _, d := range []string{"huggingface.co", "hf.co"} {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("hf: more than 5 redirects from %s", via[0].URL)
	}
	if !c.allowedHost(req.URL.Host) {
		return fmt.Errorf("%w: %s", ErrRedirect, req.URL.Host)
	}
	if base, err := url.Parse(c.BaseURL); err == nil && base.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("%w: %s is not https", ErrRedirect, req.URL)
	}
	return nil
}

// throttle keeps MinInterval between requests.
func (c *Client) throttle(ctx context.Context) error {
	c.mu.Lock()
	wait := time.Until(c.last.Add(c.MinInterval))
	c.last = time.Now()
	if wait > 0 {
		c.last = c.last.Add(wait)
	}
	c.mu.Unlock()
	if wait > 0 {
		return c.Sleep(ctx, wait)
	}
	return ctx.Err()
}

// do sends req (rebuilt by mk for each attempt) with throttling and bounded
// retries. The caller closes the returned body.
func (c *Client) do(ctx context.Context, mk func() (*http.Request, error)) (*http.Response, error) {
	attempts := c.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	transportOnly := true // every attempt so far failed below HTTP
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := c.throttle(ctx); err != nil {
			return nil, err
		}
		req, err := mk()
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.UserAgent)
		c.count(func(s *Stats) { s.Requests++ })
		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, ErrRedirect) {
				return nil, err
			}
			if attempt == attempts && transportOnly {
				return nil, fmt.Errorf("%w (%d attempts): %v", ErrUnreachable, attempts, err)
			}
			lastErr = fmt.Errorf("hf: %s: %w", req.URL, err)
			if attempt < attempts {
				c.count(func(s *Stats) { s.Retries++ })
				if err := c.Sleep(ctx, backoff(attempt)); err != nil {
					return nil, err
				}
			}
			continue
		}
		switch {
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable ||
			resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout:
			transportOnly = false
			wait, stated := retryAfter(resp.Header, time.Now())
			body := snippet(resp.Body)
			resp.Body.Close()
			if !stated {
				wait = backoff(attempt)
			}
			lastErr = &StatusError{Status: resp.StatusCode, URL: req.URL.String(), Body: body}
			if resp.StatusCode == http.StatusTooManyRequests {
				lastErr.(*StatusError).err = ErrRateLimited
			}
			if wait > c.MaxWait {
				return nil, fmt.Errorf("%w: asked to wait %s (longest honoured: %s)", ErrRateLimited, wait.Round(time.Second), c.MaxWait)
			}
			if attempt < attempts {
				c.Log.Info("hugging face asked to wait", "status", resp.StatusCode, "wait", wait.Round(time.Millisecond), "url", req.URL.String())
				c.count(func(s *Stats) { s.Retries++ })
				if err := c.Sleep(ctx, wait); err != nil {
					return nil, err
				}
			}
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

// backoff is 1s, 2s, 4s, ... for retries the server gave no wait for.
func backoff(attempt int) time.Duration {
	return time.Duration(1<<(attempt-1)) * time.Second
}

// retryAfter reads how long the server asked to wait: Retry-After (seconds
// or an HTTP date), else the "t=" (seconds to reset) of Hugging Face's
// RateLimit header ("api";r=0;t=55).
func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second, true
		}
		if t, err := http.ParseTime(v); err == nil {
			d := t.Sub(now)
			if d < 0 {
				d = 0
			}
			return d, true
		}
	}
	if v := h.Get("RateLimit"); v != "" {
		for _, part := range strings.Split(v, ";") {
			if k, val, ok := strings.Cut(strings.TrimSpace(part), "="); ok && k == "t" {
				if secs, err := strconv.Atoi(val); err == nil && secs >= 0 {
					return time.Duration(secs) * time.Second, true
				}
			}
		}
	}
	return 0, false
}

func snippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 300))
	return strings.TrimSpace(string(b))
}

func statusError(resp *http.Response) error {
	e := &StatusError{Status: resp.StatusCode, URL: resp.Request.URL.String(), Body: snippet(resp.Body)}
	switch resp.StatusCode {
	case http.StatusNotFound:
		e.err = ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		e.err = ErrGated
	case http.StatusTooManyRequests:
		e.err = ErrRateLimited
	}
	return e
}

// ModelInfo is the part of the Hub's model-info answer the catalogue uses.
type ModelInfo struct {
	ID           string    `json:"id"`
	SHA          string    `json:"sha"` // the commit the listing describes
	LastModified string    `json:"lastModified"`
	Private      bool      `json:"private"`
	Gated        Gated     `json:"gated"`
	Disabled     bool      `json:"disabled"`
	CardData     CardData  `json:"cardData"`
	GGUF         *GGUFInfo `json:"gguf"`
	Siblings     []Sibling `json:"siblings"`
}

// CardData is the model card's front matter, as far as the catalogue reads it.
type CardData struct {
	License     any    `json:"license"` // a string, or a list of them
	LicenseName string `json:"license_name"`
	LicenseLink string `json:"license_link"`
}

// GGUFInfo is what the Hub itself extracted from the repo's GGUF files.
type GGUFInfo struct {
	Total         uint64 `json:"total"` // parameter count, from the tensor shapes
	Architecture  string `json:"architecture"`
	ContextLength uint64 `json:"context_length"`
}

// Sibling is one file in the repo.
type Sibling struct {
	RFilename string   `json:"rfilename"`
	Size      uint64   `json:"size"`
	BlobID    string   `json:"blobId"`
	LFS       *LFSInfo `json:"lfs"`
}

// LFSInfo identifies a large file's content.
type LFSInfo struct {
	SHA256 string `json:"sha256"`
	Size   uint64 `json:"size"`
}

// Gated is the Hub's "gated" field: false, or "auto" / "manual".
type Gated string

// UnmarshalJSON accepts both shapes.
func (g *Gated) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case bool:
		if x {
			*g = "true"
		} else {
			*g = ""
		}
	case string:
		*g = Gated(x)
	case nil:
		*g = ""
	default:
		return fmt.Errorf("hf: unexpected gated value %s", b)
	}
	return nil
}

// Listing is one model-info answer.
type Listing struct {
	Info        *ModelInfo
	Body        []byte // the JSON as the Hub sent it (or as the cache held it on a 304)
	ETag        string
	NotModified bool // the Hub answered 304 to the cached ETag; Body is the cached one
}

// ModelInfo fetches repo's listing. With etag and cached (the previous
// answer), an unchanged repo is a 304 and cached is reused.
func (c *Client) ModelInfo(ctx context.Context, repo, etag string, cached []byte) (*Listing, error) {
	u := strings.TrimRight(c.BaseURL, "/") + "/api/models/" + escapeRepo(repo) + "?blobs=true"
	resp, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if etag != "" && len(cached) > 0 {
			req.Header.Set("If-None-Match", etag)
		}
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out := &Listing{ETag: resp.Header.Get("ETag")}
	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		c.count(func(s *Stats) { s.BytesRead += int64(len(body)) })
		if err != nil {
			return nil, fmt.Errorf("hf: reading the listing of %s: %w", repo, err)
		}
		out.Body = body
	case http.StatusNotModified:
		c.count(func(s *Stats) { s.NotModified++ })
		out.Body, out.NotModified = cached, true
		if out.ETag == "" {
			out.ETag = etag
		}
	default:
		return nil, statusError(resp)
	}
	var info ModelInfo
	if err := json.Unmarshal(out.Body, &info); err != nil {
		return nil, fmt.Errorf("hf: the listing of %s is not the JSON expected: %w", repo, err)
	}
	if info.Disabled {
		return nil, fmt.Errorf("hf: %s has been disabled on Hugging Face: %w", repo, ErrNotFound)
	}
	if info.Private || info.Gated != "" {
		return nil, fmt.Errorf("hf: %s: %w", repo, ErrGated)
	}
	out.Info = &info
	return out, nil
}

// escapeRepo escapes each path element of "owner/name".
func escapeRepo(repo string) string {
	owner, name, _ := strings.Cut(repo, "/")
	return url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// escapePath escapes each element of a file path in a repo.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// ReadHeader reads the GGUF header of path in repo at revision (a commit
// sha, so the header matches the listing it came from) with range requests
// and parses it as it streams. size is the file's size from the listing
// (the reader never asks past it). It returns the header and the bytes
// downloaded.
func (c *Client) ReadHeader(ctx context.Context, repo, revision, path string, size uint64, opts gguf.Options) (*gguf.Header, int64, error) {
	if revision == "" {
		revision = "main"
	}
	u := strings.TrimRight(c.BaseURL, "/") + "/" + escapeRepo(repo) + "/resolve/" + url.PathEscape(revision) + "/" + escapePath(path)
	rr := &rangeReader{c: c, ctx: ctx, url: u, size: int64(size), chunk: c.FirstChunk, max: c.MaxChunk}
	h, err := gguf.Parse(rr, opts)
	if err != nil {
		if rr.err != nil && !errors.Is(err, gguf.ErrMalformed) {
			// The parse failed because the transfer did: report that.
			return nil, rr.read, rr.err
		}
		return nil, rr.read, fmt.Errorf("hf: %s/%s: %w", repo, path, err)
	}
	return h, rr.read, nil
}

// rangeReader reads a remote file from byte 0 as a sequence of range
// requests, each twice the size of the last, fetching only as much as the
// reader downstream asks for.
type rangeReader struct {
	c     *Client
	ctx   context.Context
	url   string
	size  int64 // 0 = unknown
	off   int64 // next byte to fetch
	chunk int64
	max   int64
	buf   []byte
	read  int64 // bytes downloaded
	err   error // the transfer error that ended the reading, if any
}

func (r *rangeReader) Read(p []byte) (int, error) {
	if len(r.buf) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		if r.size > 0 && r.off >= r.size {
			return 0, io.EOF
		}
		if err := r.fetch(); err != nil {
			r.err = err
			return 0, err
		}
		if len(r.buf) == 0 {
			return 0, io.EOF
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func (r *rangeReader) fetch() error {
	end := r.off + r.chunk - 1
	if r.size > 0 && end >= r.size {
		end = r.size - 1
	}
	want := end - r.off + 1
	resp, err := r.c.do(r.ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.off, end))
		// A compressed answer to a range request makes the byte offsets
		// meaningless; ask for the bytes as stored.
		req.Header.Set("Accept-Encoding", "identity")
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		if start, ok := contentRangeStart(resp.Header.Get("Content-Range")); !ok || start != r.off {
			return fmt.Errorf("hf: %s answered a range request for byte %d with Content-Range %q", r.url, r.off, resp.Header.Get("Content-Range"))
		}
	case http.StatusRequestedRangeNotSatisfiable:
		r.buf = nil
		return nil // past the end: EOF
	case http.StatusOK:
		// A server may answer with the whole file when the range asked for
		// all of it; that is fine for a file no bigger than this chunk. For
		// anything larger the whole file is on its way: do not read it.
		if r.off != 0 || resp.ContentLength < 0 || resp.ContentLength > want {
			return fmt.Errorf("%w (%s)", ErrRangeIgnored, r.url)
		}
		r.size = resp.ContentLength
	default:
		return statusError(resp)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, want))
	r.read += int64(len(b))
	r.c.count(func(s *Stats) { s.BytesRead += int64(len(b)) })
	if err != nil {
		return fmt.Errorf("hf: reading %s: %w", r.url, err)
	}
	r.buf = b
	r.off += int64(len(b))
	if r.chunk < r.max {
		r.chunk *= 2
		if r.chunk > r.max {
			r.chunk = r.max
		}
	}
	return nil
}

// contentRangeStart reads the first byte of "bytes 0-65535/12345678".
func contentRangeStart(v string) (int64, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(v), "bytes ")
	if !ok {
		return 0, false
	}
	first, _, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(first, 10, 64)
	return n, err == nil
}
