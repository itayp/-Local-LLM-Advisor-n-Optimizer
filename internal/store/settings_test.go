package store

import (
	"context"
	"testing"
)

func TestSettingRoundTrips(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	if _, ok, err := s.Setting(ctx, "onboarding.completed"); err != nil {
		t.Fatalf("Setting (unset): %v", err)
	} else if ok {
		t.Fatal("Setting reported ok for a key that was never set")
	}

	if err := s.SetSetting(ctx, "onboarding.completed", "1"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	v, ok, err := s.Setting(ctx, "onboarding.completed")
	if err != nil {
		t.Fatalf("Setting: %v", err)
	}
	if !ok || v != "1" {
		t.Fatalf("Setting = %q, %v, want \"1\", true", v, ok)
	}
}

func TestSetSettingOverwritesInPlace(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	if err := s.SetSetting(ctx, "k", "first"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.SetSetting(ctx, "k", "second"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	v, ok, err := s.Setting(ctx, "k")
	if err != nil || !ok || v != "second" {
		t.Fatalf("Setting = %q, %v, %v, want \"second\", true, nil", v, ok, err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = 'k'`).Scan(&n); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("settings has %d rows for key \"k\", want 1 (overwrite in place, no history)", n)
	}
}
