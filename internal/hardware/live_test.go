package hardware

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"testing"
	"time"
)

// TestDetectOnThisMachine runs the real detector on whatever machine runs
// the tests — each CI runner exercises its own OS path against the real
// tools. It asserts only what must hold everywhere: no failure, the
// invariants, and the facts every runner can read about itself.
// ADVISOR_PRINT_PROFILE=1 prints the profile.
func TestDetectOnThisMachine(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real system tools")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, err := Detect(ctx)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	checkInvariants(t, p)
	if p.OS != runtime.GOOS {
		t.Errorf("OS %q, want %q", p.OS, runtime.GOOS)
	}
	if os.Getenv("ADVISOR_PRINT_PROFILE") != "" {
		b, _ := json.MarshalIndent(p, "", "  ")
		t.Logf("%s", b)
	}
}
