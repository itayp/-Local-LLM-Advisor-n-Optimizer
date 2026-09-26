package winapp

import "testing"

// TestRegisterIdentityNeverErrorsHere runs registerIdentity for real: a
// no-op returning nil on every OS but Windows, and — when this test
// actually runs on a Windows CI runner — the real
// SetCurrentProcessExplicitAppUserModelID + registry write, both HKCU-only
// and safe to repeat, so there is nothing to fake here.
func TestRegisterIdentityNeverErrorsHere(t *testing.T) {
	if err := RegisterIdentity(); err != nil {
		t.Fatalf("RegisterIdentity: %v", err)
	}
}

func TestAUMIDIsStable(t *testing.T) {
	if AUMID == "" {
		t.Fatal("AUMID is empty")
	}
}
