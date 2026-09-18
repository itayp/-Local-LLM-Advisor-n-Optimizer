// Package server is the daemon's HTTP surface: a JSON API under /api/ and
// the embedded React UI for everything else, on the loopback interface and
// nowhere else.
//
// Product rule 7 is enforced here, not documented: the listen address is
// the constant LoopbackHost, the only way to construct a listener is
// Listen(port), and Serve refuses a listener whose address is not loopback.
// There is no flag, no environment variable and no config key for the bind
// address, and there must never be one.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"advisor/internal/backend"
	"advisor/internal/store"
	"advisor/internal/version"
)

// LoopbackHost is the only address the daemon binds to. A constant, not a
// flag (BUILD_PLAN.md, step 1).
const LoopbackHost = "127.0.0.1"

// DefaultPort is the port the daemon tries first. If it is taken, the
// daemon falls back to a port the OS picks and prints it; the browser is
// opened at whichever it got.
const DefaultPort = 27182

// ErrNotLoopback is returned by Serve when handed a listener that is not
// bound to LoopbackHost.
var ErrNotLoopback = errors.New("server: refusing to serve on a non-loopback address")

// Listen opens a TCP listener on LoopbackHost:port. port 0 asks the OS for
// a free port. It cannot be asked to bind anywhere else.
func Listen(port int) (net.Listener, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("server: port %d out of range", port)
	}
	// "tcp4" on purpose: the constant is an IPv4 loopback address, and the
	// Host-header check below admits only that address and "localhost".
	return net.Listen("tcp4", net.JoinHostPort(LoopbackHost, strconv.Itoa(port)))
}

// IsLoopback reports whether addr is bound to LoopbackHost.
func IsLoopback(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || tcp.IP == nil {
		return false
	}
	return tcp.IP.Equal(net.ParseIP(LoopbackHost))
}

// Health is the payload of GET /api/health.
type Health struct {
	Version   string `json:"version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"go_version"`
}

// Server serves the API and the UI.
type Server struct {
	log      *slog.Logger
	mux      *http.ServeMux
	apiPaths map[string]bool // bare paths that already have a 405 fallback
	started  time.Time
	store    *store.Store // nil in tests that need no persistence
	hw       hardwareState

	// backendList is where RecordBackends gets the runtimes to check.
	// Defaults to the package-level registry (backend.All — whatever
	// imported itself in with an init(), "ollama" today); tests set it
	// directly to a fake, the same seam hardwareWait gives RecordHardware.
	backendList func() []backend.Backend
}

// New builds a Server. Dependencies are added as parameters by the steps
// that need them: step 2 adds the store (hardware profiles and their
// history). st may be nil, for tests that need no persistence.
func New(log *slog.Logger, st *store.Store) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{log: log, mux: http.NewServeMux(), started: time.Now(), store: st, backendList: backend.All}
	s.hw.ready = make(chan struct{})
	s.routes()
	return s
}

func (s *Server) routes() {
	// The API. Every endpoint is registered through api(), which pairs the
	// method-qualified pattern with a 405 for the other methods.
	s.api("GET /api/health", s.handleHealth)
	s.api("GET /api/hardware", s.handleHardware)
	s.api("GET /api/hardware/history", s.handleHardwareHistory)
	s.api("GET /api/hardware/profiles/{id}", s.handleHardwareProfile)
	s.api("GET /api/backends", s.handleBackends)
	s.api("GET /api/models/installed", s.handleModelsInstalled)
	// Anything else under /api/ is a JSON 404, never the SPA's index.html.
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such API endpoint")
	})
	// The UI: static files from the embedded build, index.html for any
	// client-side route.
	s.mux.Handle("/", uiHandler())
}

// api registers a method-qualified pattern ("GET /api/health") and, once
// per path, a bare-path handler that answers 405 — otherwise the "/api/"
// catch-all would turn a wrong method into a 404.
func (s *Server) api(pattern string, h http.HandlerFunc) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || method == "" || !strings.HasPrefix(path, "/api/") {
		panic("server: api pattern must be \"METHOD /api/...\": " + pattern)
	}
	s.mux.HandleFunc(pattern, h)
	if s.apiPaths == nil {
		s.apiPaths = map[string]bool{}
	}
	if !s.apiPaths[path] {
		s.apiPaths[path] = true
		s.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed on this endpoint")
		})
	}
}

// Handler returns the full handler chain, for Serve and for tests.
func (s *Server) Handler() http.Handler {
	return s.hostCheck(s.noStore(s.mux))
}

// Serve serves on l until ctx is cancelled. It refuses a non-loopback
// listener.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	if !IsLoopback(l.Addr()) {
		_ = l.Close()
		return fmt.Errorf("%w: %s", ErrNotLoopback, l.Addr())
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(l) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		<-errc
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, Health{
		Version:   version.Version,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: version.GoVersion(),
	})
}

// hostCheck rejects requests whose Host header is not the loopback address
// or "localhost". Binding to 127.0.0.1 keeps the network out; this keeps a
// web page in the user's browser from reaching the daemon through DNS
// rebinding (a hostname the attacker controls that resolves to 127.0.0.1).
// Origin, when a browser sends one, must agree.
func (s *Server) hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedHost(r.Host) {
			s.log.Warn("rejected request with foreign Host header", "host", r.Host, "path", r.URL.Path)
			writeError(w, http.StatusMisdirectedRequest, "bad_host",
				"this daemon only answers requests addressed to 127.0.0.1 or localhost")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !allowedOrigin(origin) {
			s.log.Warn("rejected cross-origin request", "origin", origin, "path", r.URL.Path)
			writeError(w, http.StatusForbidden, "bad_origin", "cross-origin requests are not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowedHost(host string) bool {
	h := host
	if strings.Contains(h, ":") {
		var err error
		h, _, err = net.SplitHostPort(host)
		if err != nil {
			return false
		}
	}
	return h == LoopbackHost || h == "localhost"
}

func allowedOrigin(origin string) bool {
	rest, ok := strings.CutPrefix(origin, "http://")
	if !ok {
		return false
	}
	return allowedHost(rest)
}

// noStore keeps API responses out of caches.
func (s *Server) noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// APIError is the body of every non-2xx API response.
type APIError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		// A figure without a Source lands here: the response fails loudly
		// instead of showing a number with no provenance.
		slog.Error("encoding API response", "err", err)
		writeError(w, http.StatusInternalServerError, "encoding", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var e APIError
	e.Error.Code = code
	e.Error.Message = message
	body, _ := json.Marshal(e)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
