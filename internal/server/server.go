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
	"advisor/internal/bench"
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

	// cat is the catalogue endpoints' state (catalog.go).
	cat catalogState

	// bench runs benchmarks (bench.go); nil without a store.
	bench *bench.Harness

	// installs and pulls track the one in-flight (or last-finished)
	// install-per-backend and model download (install.go, pull.go) — the
	// UI button build-plan step 7 adds. In memory only; see their doc
	// comments for why.
	installs installTracker
	pulls    pullTracker

	// open asks the OS to open a folder in its file manager (settings.go's
	// two "open" buttons). Defaults to openInFileManager; tests set it to
	// a fake so they never launch a real file manager.
	open func(dir string) error
}

// New builds a Server. Dependencies are added as parameters by the steps
// that need them: step 2 adds the store (hardware profiles and their
// history). st may be nil, for tests that need no persistence.
func New(log *slog.Logger, st *store.Store) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{log: log, mux: http.NewServeMux(), started: time.Now(), store: st, backendList: backend.All, open: openInFileManager}
	s.hw.ready = make(chan struct{})
	s.cat.init()
	if st != nil {
		h, err := bench.New(st, log)
		if err != nil {
			// The suite is embedded: this is a build that cannot benchmark,
			// and the endpoints say so.
			log.Error("loading the benchmark suite", "err", err)
		} else {
			s.bench = h
		}
	}
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
	s.api("GET /api/catalog", s.handleCatalog)
	s.api("POST /api/catalog/refresh", s.handleCatalogRefresh)
	s.api("GET /api/catalog/unknown", s.handleCatalogUnknown)
	s.api("GET /api/catalog/status", s.handleCatalogStatus) // has the model list been fetched; a running fetch's progress
	s.api("GET /api/recommend", s.handleRecommend)
	s.api("GET /api/models/{id}/fit", s.handleModelFit)
	s.api("GET /api/models/{id}/detail", s.handleModelDetail) // step 9b: public data and this machine, side by side, apart
	s.api("POST /api/bench", s.handleBenchStart)
	s.api("GET /api/bench/{id}", s.handleBenchRun)
	s.api("POST /api/bench/{id}/cancel", s.handleBenchCancel)
	s.api("GET /api/bench/{id}/progress", s.handleBenchProgress) // the progress as one JSON answer, for a browser whose stream does not arrive
	// Literal paths beside the {id} wildcard: the wildcard's own fallback
	// answers a wrong method on them with 405 (a second fallback for the
	// literal path would conflict with "GET /api/bench/{id}").
	s.apiLiteral("GET /api/bench/plan", s.handleBenchPlan)
	s.apiLiteral("GET /api/bench/history", s.handleBenchHistory)
	s.apiLiteral("GET /api/bench/models", s.handleBenchModels) // what can be tested: installed, and what the list has that is not
	// First-run onboarding (build-plan step 7): whether it has run once.
	s.api("GET /api/onboarding", s.handleOnboardingStatus)
	s.api("POST /api/onboarding/complete", s.handleOnboardingComplete)
	// Ollama install/start, and a model pull — both a button in the
	// onboarding flow starts, follows by polling, and (pull) can cancel.
	s.api("GET /api/backends/{name}/install-size", s.handleBackendInstallSize)
	s.api("GET /api/backends/{name}/install", s.handleBackendInstallStatus)
	s.api("POST /api/backends/{name}/install", s.handleBackendInstallStart)
	s.api("POST /api/backends/{name}/start", s.handleBackendStart)
	s.api("GET /api/models/pull", s.handlePullStatus)
	s.api("POST /api/models/pull", s.handlePullStart)
	s.api("POST /api/models/pull/cancel", s.handlePullCancel)
	// Chat apps already on this machine (build-plan step 7, D-4): the
	// advisor only detects and hands off, never installs one.
	s.api("GET /api/chatapps", s.handleChatApps)
	// Settings (build-plan step 8): the durable, machine-wide Advanced
	// toggle, and a button that opens each of the two folders D-16 names.
	s.api("GET /api/settings", s.handleSettingsGet)
	s.api("PUT /api/settings", s.handleSettingsPut)
	s.api("POST /api/settings/open-data-dir", s.handleOpenDataDir)
	s.api("POST /api/settings/open-models-dir", s.handleOpenModelsDir)
	// Removing an installed model (the Models screen's "Remove" button,
	// build-plan step 8): the backend deletes it, then its inventory is
	// re-read the same way RecordBackends does after every check.
	s.api("POST /api/backends/{name}/models/remove", s.handleModelRemove)
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

// apiLiteral registers a method-qualified literal path that sits beside a
// wildcard sibling registered through api() ("GET /api/bench/history"
// beside "GET /api/bench/{id}"): the sibling's bare-path fallback already
// answers other methods with 405.
func (s *Server) apiLiteral(pattern string, h http.HandlerFunc) {
	if _, path, ok := strings.Cut(pattern, " "); !ok || !strings.HasPrefix(path, "/api/") {
		panic("server: api pattern must be \"METHOD /api/...\": " + pattern)
	}
	s.mux.HandleFunc(pattern, h)
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
