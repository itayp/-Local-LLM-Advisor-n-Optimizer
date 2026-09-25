package watch

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/catalog/hf"
	"advisor/internal/estimate"
	"advisor/internal/hardware"
	"advisor/internal/recommend"
	"advisor/internal/store"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "advisor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// testEntry is one minimal, hand-built catalogue size: just enough for
// Engine.DefaultFile to resolve a file (Run never needs it to actually
// fit — checkOne's tests exercise that with a hand-built Result instead of
// asking Engine.Recommend to fit real memory arithmetic against a made-up
// header).
func testEntry(id int64, familyID, name string) recommend.Entry {
	return recommend.Entry{
		FamilyID: familyID, DisplayName: name, Purposes: []catalog.Purpose{catalog.PurposeChat},
		Model: catalog.Model{
			ID: id, FamilyID: familyID, Present: true,
			Files: []catalog.File{{ID: id * 10, ModelID: id, Present: true, Role: catalog.RoleModel, Quant: "Q4_K_M", Bytes: 1}},
		},
	}
}

func mustEngine(t *testing.T, entries []recommend.Entry) *recommend.Engine {
	t.Helper()
	e, err := recommend.New(entries)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// fakeNotifier records every call instead of touching a real OS notifier.
type fakeNotifier struct {
	notes []Notification
	err   error
}

func (f *fakeNotifier) Notify(_ context.Context, n Notification) error {
	f.notes = append(f.notes, n)
	return f.err
}

func TestRunTheMasterSwitchDoesNothing(t *testing.T) {
	st := newTestStore(t)
	rep, err := Run(context.Background(), Options{
		Store: st, Engine: mustEngine(t, []recommend.Entry{testEntry(1, "fam", "Test 8B")}),
		Settings: Settings{Enabled: false, Mode: NotifyOn}, Trigger: "test", Log: quiet(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 0 || rep.Notified != 0 || rep.Suppressed != 0 || len(rep.Entries) != 0 {
		t.Fatalf("Enabled=false must check nothing: %+v", rep)
	}
	runs, err := st.RecentWatchRuns(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("Enabled=false must not even record a run: %d", len(runs))
	}
}

// TestRunNotifyNeverStillChecksAndLogs is the safe end-to-end path through
// Run (no real Fit/Recommend arithmetic: NotifyNever short-circuits before
// Engine.DefaultFile or Engine.Recommend are ever reached — see checkOne).
// It exercises the orchestration real Run() does: the once-ever store
// bookkeeping, and that turning notifications off logs every curated size
// as suppressed rather than skipping them.
func TestRunNotifyNeverStillChecksAndLogs(t *testing.T) {
	st := newTestStore(t)
	entries := []recommend.Entry{testEntry(1, "fam", "Test 8B"), testEntry(2, "fam", "Test 70B")}
	opts := Options{
		Store: st, Engine: mustEngine(t, entries),
		Settings: Settings{Enabled: true, Mode: NotifyNever}, Trigger: "test", Log: quiet(),
		Now: func() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) },
	}
	rep, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 2 || rep.Suppressed != 2 || rep.Notified != 0 {
		t.Fatalf("report: %+v", rep)
	}
	for _, e := range rep.Entries {
		if e.Outcome != OutcomeSuppressed || e.Detail != "notifications are turned off" {
			t.Errorf("entry %+v", e)
		}
	}

	// Nothing was ever notified, so turning notifications back on later
	// must not find anything "already seen" — every model still gets its
	// one fair look (Settings.Mode governs the popup, not the "once ever"
	// allowance).
	states, err := st.WatchStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for key, st := range states {
		if st.NotifiedAt != "" {
			t.Errorf("%s: NotifyNever must never set notified_at, got %q", key, st.NotifiedAt)
		}
	}

	runs, err := st.RecentWatchRuns(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Checked != 2 {
		t.Fatalf("expected one recorded run with checked=2, got %+v", runs)
	}
}

// TestRunSkipsAModelLeftFamiliesYAML checks that a size whose Present flag
// is false (it left the curated catalogue) is never checked.
func TestRunSkipsAModelLeftFamiliesYAML(t *testing.T) {
	st := newTestStore(t)
	e := testEntry(1, "fam", "Gone")
	e.Model.Present = false
	rep, err := Run(context.Background(), Options{
		Store: st, Engine: mustEngine(t, []recommend.Entry{e}),
		Settings: Settings{Enabled: true, Mode: NotifyNever}, Trigger: "test", Log: quiet(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 0 || len(rep.Entries) != 0 {
		t.Fatalf("a size no longer present must not be checked: %+v", rep)
	}
}

// TestRunFlagsANewMaintainerRepoOnceEver drives the maintainer half of Run
// against a fake Hugging Face author-listing endpoint (no fit arithmetic
// involved at all), and checks a repo is flagged once, never twice.
func TestRunFlagsANewMaintainerRepoOnceEver(t *testing.T) {
	srv := newAuthorFake(t, map[string][]fakeAuthorRepo{
		"Qwen": {{ID: "Qwen/Qwen4-8B"}, {ID: "Qwen/Qwen3.5-9B"}}, // the second is already in the test catalogue below
	})
	cat := &catalog.Catalogue{Families: []catalog.Family{{
		ID: "qwen3.5", Sizes: []catalog.Size{{HFBaseRepo: "Qwen/Qwen3.5-9B", HFRepo: "bartowski/Qwen_Qwen3.5-9B-GGUF"}},
	}}}
	client := srv.client(t)

	st := newTestStore(t)
	opts := Options{
		Store: st, Catalogue: cat, HF: client, Config: DefaultConfig(),
		Engine: mustEngine(t, nil), Settings: Settings{Enabled: true, Mode: NotifyNever}, Trigger: "test", Log: quiet(),
	}
	rep, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Flagged != 1 || len(rep.Entries) != 1 || rep.Entries[0].Key != "repo:Qwen/Qwen4-8B" {
		t.Fatalf("expected exactly the new repo flagged once: %+v", rep.Entries)
	}

	// A second run must not flag it again.
	rep2, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Flagged != 0 {
		t.Fatalf("a repo already flagged must not be flagged again: %+v", rep2.Entries)
	}
}

// TestRunSuppressesWithTheEngineNoteWhenTheMachineCannotRunAnything drives
// Run's Mode=NotifyOn path through the real Engine.Recommend, on a golden
// hardware profile the estimator refuses outright (too old an OS) — a path
// Recommend takes before it ever touches a candidate's memory arithmetic
// (estimate.Placement.Blocked), so it is safe to exercise for real.
func TestRunSuppressesWithTheEngineNoteWhenTheMachineCannotRunAnything(t *testing.T) {
	st := newTestStore(t)
	entries := []recommend.Entry{testEntry(1, "fam", "Test 8B")}
	m := goldenMachine(t, "MacOSTooOldForOllama")
	notifier := &fakeNotifier{}
	rep, err := Run(context.Background(), Options{
		Store: st, Engine: mustEngine(t, entries), Machine: m,
		Purposes: []catalog.Purpose{catalog.PurposeChat},
		Settings: Settings{Enabled: true, Mode: NotifyOn}, Notifier: notifier, Trigger: "test", Log: quiet(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Notified != 0 || rep.Suppressed != 1 || len(notifier.notes) != 0 {
		t.Fatalf("a machine nothing can run on must suppress, never notify: %+v (notes=%d)", rep, len(notifier.notes))
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Detail == "" {
		t.Fatalf("a suppressed entry must say why: %+v", rep.Entries)
	}
}

// goldenMachine loads one of internal/hardware's golden profiles — the same
// small helper internal/estimate and internal/recommend's own tests use.
func goldenMachine(t *testing.T, name string) estimate.Machine {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "hardware", "testdata", "golden", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var p hardware.Profile
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return estimate.Machine{Profile: p}
}

// fakeAuthorRepo is the one field the maintainer scan reads off a repo the
// fake Hub answers with (maintainers.go skips private/disabled/gated repos;
// a test that needs one sets it explicitly).
type fakeAuthorRepo struct {
	ID       string
	Private  bool
	Disabled bool
	Gated    string
}

// authorFake is a fake of GET /api/models?author=... that can answer for
// more than one owner — internal/catalog/hf's own authorHub (authors_test.go)
// is unexported and scoped to a single author, so internal/watch's tests,
// which drive checkMaintainers across every curated family's maintainer, get
// their own copy of the same small hub rather than reaching into hf's
// internal test file.
type authorFake struct {
	t     *testing.T
	srv   *httptest.Server
	repos map[string][]fakeAuthorRepo
}

func newAuthorFake(t *testing.T, repos map[string][]fakeAuthorRepo) *authorFake {
	t.Helper()
	f := &authorFake{t: t, repos: repos}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *authorFake) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/models" {
		http.NotFound(w, r)
		return
	}
	repos := f.repos[r.URL.Query().Get("author")]
	out := make([]hf.AuthorRepo, 0, len(repos))
	for _, rp := range repos {
		out = append(out, hf.AuthorRepo{ID: rp.ID, Private: rp.Private, Disabled: rp.Disabled, Gated: hf.Gated(rp.Gated)})
	}
	w.Header().Set("ETag", `"v1"`)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// client returns an *hf.Client pointed at the fake, built the same way
// internal/catalog/hf's own authorHub.client() builds one for its tests.
func (f *authorFake) client(t *testing.T) *hf.Client {
	t.Helper()
	c := hf.New("advisor-test/0")
	c.BaseURL = f.srv.URL
	c.MinInterval = 0
	c.Log = quiet()
	c.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	return c
}
