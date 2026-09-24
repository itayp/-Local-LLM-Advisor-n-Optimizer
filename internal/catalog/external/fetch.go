package external

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fetcher is the one HTTP client every external source goes through. It is
// the same kind of polite as internal/catalog/hf (D-34): one request at a
// time with a minimum gap, 429/5xx retried after the wait the server asks
// for (bounded), a bounded number of attempts, no token and no cookies ever
// sent, and a User-Agent naming the advisor and its version. It connects
// only to Hosts — the enabled sources' hosts from external.yaml, themselves
// bounded by PermittedHosts — and follows a redirect only to one of them.
// Nothing about the machine or the user is in any request: the requests are
// a function of families.yaml, aliases.yaml and external.yaml alone
// (research/EXTERNAL_SOURCES.md, criterion 4).
type Fetcher struct {
	HTTP        *http.Client
	UserAgent   string
	Log         *slog.Logger
	MinInterval time.Duration
	MaxWait     time.Duration
	MaxAttempts int
	// MaxBytes bounds one answer's body; a larger answer is an error, never
	// a truncated parse.
	MaxBytes int64
	// LoadingWait is the first wait after an answer that says the source is
	// still loading its data (doubled on each later attempt), longer than
	// the ordinary backoff because an index takes a while to build.
	LoadingWait time.Duration
	// CaptureDir, when set, receives every 200 answer's body as a file named
	// after its URL: how a curator captures real responses as test fixtures
	// (`advisor catalog external -capture DIR`).
	CaptureDir string
	// Sleep waits for d or until ctx is done. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error

	hosts map[string]bool

	mu   sync.Mutex
	last time.Time

	statsMu sync.Mutex
	stats   Stats
}

// Stats counts what the fetcher did, for the report.
type Stats struct {
	Requests    int   `json:"requests" source:"n/a"`     // a count
	NotModified int   `json:"not_modified" source:"n/a"` // a count
	Retries     int   `json:"retries" source:"n/a"`      // a count
	BytesRead   int64 `json:"bytes_read" source:"n/a"`   // a count of bytes downloaded
}

// Errors a source can act on.
var (
	ErrNotFound    = errors.New("external: not found")
	ErrRateLimited = errors.New("external: rate limited")
	ErrUnreachable = errors.New("external: the source could not be reached")
	ErrHost        = errors.New("external: host not permitted")
	ErrTooLarge    = errors.New("external: the answer is larger than the advisor reads")
)

// StatusError is an HTTP answer the fetcher could not use.
type StatusError struct {
	Status int
	URL    string
	Body   string
	err    error
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("external: %s answered HTTP %d", e.URL, e.Status)
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

func (e *StatusError) Unwrap() error { return e.err }

// NewFetcher returns a fetcher for hosts (exact host names; a test server's
// "127.0.0.1:port" is a host too).
func NewFetcher(userAgent string, hosts []string) *Fetcher {
	f := &Fetcher{
		UserAgent:   userAgent,
		Log:         slog.Default(),
		MinInterval: 250 * time.Millisecond,
		MaxWait:     2 * time.Minute,
		MaxAttempts: 4,
		LoadingWait: 5 * time.Second,
		MaxBytes:    64 << 20,
		Sleep:       sleepCtx,
		hosts:       map[string]bool{},
	}
	for _, h := range hosts {
		f.hosts[strings.ToLower(h)] = true
	}
	f.HTTP = &http.Client{Timeout: 2 * time.Minute, CheckRedirect: f.checkRedirect}
	return f
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

// Allowed reports whether the fetcher may connect to rawURL's host.
func (f *Fetcher) Allowed(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return f.hosts[strings.ToLower(u.Host)] || f.hosts[strings.ToLower(u.Hostname())]
}

func (f *Fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("external: more than 5 redirects from %s", via[0].URL)
	}
	if !f.Allowed(req.URL.String()) {
		return fmt.Errorf("%w: a redirect to %s", ErrHost, req.URL.Host)
	}
	if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("%w: a redirect to %s is not https", ErrHost, req.URL)
	}
	return nil
}

// Stats returns a snapshot of the counters.
func (f *Fetcher) Stats() Stats {
	f.statsMu.Lock()
	defer f.statsMu.Unlock()
	return f.stats
}

func (f *Fetcher) count(fn func(*Stats)) {
	f.statsMu.Lock()
	fn(&f.stats)
	f.statsMu.Unlock()
}

// Validators make a request conditional.
type Validators struct {
	ETag         string
	LastModified string
}

// Response is one usable answer.
type Response struct {
	Body         []byte
	ETag         string
	LastModified string
	NotModified  bool // 304: the caller keeps what it stored
}

// Get fetches rawURL, conditionally when v is set.
func (f *Fetcher) Get(ctx context.Context, rawURL string, v Validators) (*Response, error) {
	if !f.Allowed(rawURL) {
		return nil, fmt.Errorf("%w: %s", ErrHost, rawURL)
	}
	attempts := max(f.MaxAttempts, 1)
	var lastErr error
	transportOnly := true
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := f.throttle(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", f.UserAgent)
		if v.ETag != "" {
			req.Header.Set("If-None-Match", v.ETag)
		}
		if v.LastModified != "" {
			req.Header.Set("If-Modified-Since", v.LastModified)
		}
		f.count(func(s *Stats) { s.Requests++ })
		resp, err := f.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, ErrHost) {
				return nil, err
			}
			lastErr = err
			if attempt == attempts && transportOnly {
				return nil, fmt.Errorf("%w (%d attempts): %v", ErrUnreachable, attempts, err)
			}
			if attempt < attempts {
				f.count(func(s *Stats) { s.Retries++ })
				if err := f.Sleep(ctx, backoff(attempt)); err != nil {
					return nil, err
				}
			}
			continue
		}
		switch resp.StatusCode {
		case http.StatusOK:
			defer resp.Body.Close()
			body, err := io.ReadAll(io.LimitReader(resp.Body, f.MaxBytes+1))
			f.count(func(s *Stats) { s.BytesRead += int64(len(body)) })
			if err != nil {
				return nil, fmt.Errorf("external: reading %s: %w", rawURL, err)
			}
			if int64(len(body)) > f.MaxBytes {
				return nil, fmt.Errorf("%w (%s, over %d MB)", ErrTooLarge, rawURL, f.MaxBytes>>20)
			}
			f.capture(rawURL, body)
			return &Response{Body: body, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}, nil
		case http.StatusNotModified:
			resp.Body.Close()
			f.count(func(s *Stats) { s.NotModified++ })
			out := &Response{NotModified: true, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}
			if out.ETag == "" {
				out.ETag = v.ETag
			}
			if out.LastModified == "" {
				out.LastModified = v.LastModified
			}
			return out, nil
		case http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout,
			http.StatusInternalServerError:
			// A 500 is retried too: Hugging Face's dataset viewer answers 500
			// "the dataset index is loading" while it rebuilds, and a page
			// read a few seconds later succeeds (the first coverage report,
			// 2026-09-24).
			transportOnly = false
			wait, stated := retryAfter(resp.Header, time.Now())
			se := &StatusError{Status: resp.StatusCode, URL: rawURL, Body: snippet(resp.Body)}
			resp.Body.Close()
			if resp.StatusCode == http.StatusTooManyRequests {
				se.err = ErrRateLimited
			}
			lastErr = se
			if !stated {
				wait = backoff(attempt)
				if strings.Contains(strings.ToLower(se.Body), "loading") {
					wait = f.LoadingWait * time.Duration(1<<(attempt-1))
				}
			}
			if wait > f.MaxWait {
				return nil, fmt.Errorf("%w: asked to wait %s", ErrRateLimited, wait.Round(time.Second))
			}
			if attempt < attempts {
				f.count(func(s *Stats) { s.Retries++ })
				if err := f.Sleep(ctx, wait); err != nil {
					return nil, err
				}
			}
			continue
		default:
			se := &StatusError{Status: resp.StatusCode, URL: rawURL, Body: snippet(resp.Body)}
			resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				se.err = ErrNotFound
			}
			return nil, se
		}
	}
	return nil, lastErr
}

func (f *Fetcher) throttle(ctx context.Context) error {
	f.mu.Lock()
	wait := time.Until(f.last.Add(f.MinInterval))
	f.last = time.Now()
	if wait > 0 {
		f.last = f.last.Add(wait)
	}
	f.mu.Unlock()
	if wait > 0 {
		return f.Sleep(ctx, wait)
	}
	return ctx.Err()
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// capture writes body to CaptureDir under a name made from the URL (no
// colons or slashes: fixture names must check out on Windows).
func (f *Fetcher) capture(rawURL string, body []byte) {
	if f.CaptureDir == "" {
		return
	}
	name := unsafeName.ReplaceAllString(strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://"), "_")
	if len(name) > 180 {
		name = name[:180]
	}
	if err := os.MkdirAll(f.CaptureDir, 0o755); err == nil {
		err = os.WriteFile(filepath.Join(f.CaptureDir, name), body, 0o644)
		if err != nil {
			f.Log.Warn("capturing a response", "url", rawURL, "err", err)
		}
	}
}

func backoff(attempt int) time.Duration { return time.Duration(1<<(attempt-1)) * time.Second }

// retryAfter reads Retry-After (seconds or a date), else the "t=" of the
// RateLimit header Hugging Face sends.
func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second, true
		}
		if t, err := http.ParseTime(v); err == nil {
			return max(t.Sub(now), 0), true
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

// describe turns a fetch error into a sentence for the report.
func describe(err error, what string) string {
	switch {
	case errors.Is(err, ErrUnreachable):
		return what + " could not be reached; the scores stored before are kept"
	case errors.Is(err, ErrRateLimited):
		return what + " is limiting requests; the scores stored before are kept, and the next refresh tries again"
	case errors.Is(err, ErrNotFound):
		return what + " answered that there is nothing at that address"
	case errors.Is(err, ErrTooLarge):
		return what + " sent more than the advisor reads; nothing from it was used"
	case errors.Is(err, ErrHost):
		return what + " tried to send the advisor to a host it does not contact (" + err.Error() + ")"
	}
	return what + ": " + err.Error()
}
