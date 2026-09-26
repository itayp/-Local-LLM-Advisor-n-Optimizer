package autostart

import "testing"

// fakeWindowsRegistry stands in for the real HKCU\...\Run key on any host
// — the point of splitting windows_logic.go out from autostart_windows.go
// (which only compiles under GOOS=windows).
type fakeWindowsRegistry struct {
	values map[string]string
}

func newFakeWindowsRegistry() *fakeWindowsRegistry {
	return &fakeWindowsRegistry{values: map[string]string{}}
}

func (f *fakeWindowsRegistry) getRunValue(name string) (string, bool, error) {
	v, ok := f.values[name]
	return v, ok, nil
}

func (f *fakeWindowsRegistry) setRunValue(name, value string) error {
	f.values[name] = value
	return nil
}

func (f *fakeWindowsRegistry) deleteRunValue(name string) error {
	delete(f.values, name) // deleting an absent key is a no-op, same as the real registry.DeleteValue's ErrNotExist handling
	return nil
}

func TestWindowsRunCommandQuotesOnlyWhenNeeded(t *testing.T) {
	cases := []struct {
		target Target
		want   string
	}{
		{Target{ExecPath: `C:\Program Files\Advisor\advisor.exe`, Args: []string{"-tray"}}, `"C:\Program Files\Advisor\advisor.exe" -tray`},
		{Target{ExecPath: `C:\advisor.exe`}, `C:\advisor.exe`},
		{Target{ExecPath: `C:\a"b.exe`}, `"C:\a\"b.exe"`},
	}
	for _, c := range cases {
		if got := windowsRunCommand(c.target); got != c.want {
			t.Errorf("windowsRunCommand(%+v) = %q, want %q", c.target, got, c.want)
		}
	}
}

func TestWindowsEnableWithWritesTheRunCommand(t *testing.T) {
	reg := newFakeWindowsRegistry()
	target := Target{ExecPath: `C:\Program Files\Local LLM Advisor\advisor.exe`, Args: []string{"-tray"}}
	if err := windowsEnableWith(reg, target); err != nil {
		t.Fatalf("windowsEnableWith: %v", err)
	}
	got, ok, err := reg.getRunValue(windowsRunValueName)
	if err != nil || !ok {
		t.Fatalf("getRunValue = %q, %v, %v", got, ok, err)
	}
	want := `"C:\Program Files\Local LLM Advisor\advisor.exe" -tray`
	if got != want {
		t.Errorf("Run value = %q, want %q", got, want)
	}
}

func TestWindowsEnabledWithReflectsPresence(t *testing.T) {
	reg := newFakeWindowsRegistry()
	if enabled, err := windowsEnabledWith(reg); err != nil || enabled {
		t.Fatalf("Enabled = %v, %v before Enable; want false, nil", enabled, err)
	}
	if err := windowsEnableWith(reg, Target{ExecPath: `C:\advisor.exe`}); err != nil {
		t.Fatal(err)
	}
	if enabled, err := windowsEnabledWith(reg); err != nil || !enabled {
		t.Fatalf("Enabled = %v, %v after Enable; want true, nil", enabled, err)
	}
	if err := windowsDisableWith(reg); err != nil {
		t.Fatal(err)
	}
	if enabled, err := windowsEnabledWith(reg); err != nil || enabled {
		t.Fatalf("Enabled = %v, %v after Disable; want false, nil", enabled, err)
	}
}

func TestWindowsDisableWithWithoutEverEnablingIsNotAnError(t *testing.T) {
	reg := newFakeWindowsRegistry()
	if err := windowsDisableWith(reg); err != nil {
		t.Fatalf("windowsDisableWith on a never-enabled entry: %v, want nil", err)
	}
}
