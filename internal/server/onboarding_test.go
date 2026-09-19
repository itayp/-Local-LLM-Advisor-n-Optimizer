package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"advisor/internal/store"
)

func TestOnboardingNotCompletedUntilMarked(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "advisor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := New(nil, st)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var status OnboardingStatus
	getJSON(t, ts.URL+"/api/onboarding", http.StatusOK, &status)
	if status.Completed {
		t.Fatalf("a fresh database should not report onboarding completed: %+v", status)
	}

	var completed OnboardingStatus
	postJSON(t, ts.URL+"/api/onboarding/complete", ``, http.StatusOK, &completed)
	if !completed.Completed || completed.CompletedAt == "" {
		t.Fatalf("POST /api/onboarding/complete = %+v, want completed with a timestamp", completed)
	}

	var again OnboardingStatus
	getJSON(t, ts.URL+"/api/onboarding", http.StatusOK, &again)
	if !again.Completed {
		t.Fatalf("onboarding should stay completed on a later GET: %+v", again)
	}
}

func TestOnboardingWithoutAStoreNeverClaimsCompleted(t *testing.T) {
	srv := New(nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var status OnboardingStatus
	getJSON(t, ts.URL+"/api/onboarding", http.StatusOK, &status)
	if status.Completed {
		t.Fatalf("without a store there is nowhere to remember completion, so it must not claim to: %+v", status)
	}

	postJSON(t, ts.URL+"/api/onboarding/complete", ``, http.StatusServiceUnavailable, nil)
}
