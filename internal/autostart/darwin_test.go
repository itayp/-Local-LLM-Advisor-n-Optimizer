package autostart

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeHomeDir(t *testing.T) func() (string, error) {
	t.Helper()
	dir := t.TempDir()
	return func() (string, error) { return dir, nil }
}

func TestDarwinEnableWritesAValidPlist(t *testing.T) {
	home := fakeHomeDir(t)
	target := Target{Name: "Local LLM Advisor", ExecPath: "/Applications/Local LLM Advisor.app/Contents/MacOS/advisor", Args: []string{"-tray"}}
	if err := darwinEnable(home, target); err != nil {
		t.Fatalf("darwinEnable: %v", err)
	}

	dir, _ := home()
	path := filepath.Join(dir, "Library", "LaunchAgents", launchAgentLabel+".plist")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the plist: %v", err)
	}

	// It must be well-formed XML — the one thing this environment can
	// actually verify without macOS's own plutil/launchd.
	if err := xml.Unmarshal(body, new(any)); err != nil {
		t.Fatalf("the plist is not well-formed XML: %v\n%s", err, body)
	}
	for _, want := range []string{
		"<key>Label</key>",
		"<string>" + launchAgentLabel + "</string>",
		"<string>/Applications/Local LLM Advisor.app/Contents/MacOS/advisor</string>",
		"<string>-tray</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("plist is missing %q:\n%s", want, body)
		}
	}
}

func TestDarwinEnableEscapesTheExecPath(t *testing.T) {
	home := fakeHomeDir(t)
	target := Target{ExecPath: `/tmp/a & b <weird>`}
	if err := darwinEnable(home, target); err != nil {
		t.Fatalf("darwinEnable: %v", err)
	}
	dir, _ := home()
	body, err := os.ReadFile(filepath.Join(dir, "Library", "LaunchAgents", launchAgentLabel+".plist"))
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(body, new(any)); err != nil {
		t.Fatalf("an unescaped path made the plist invalid XML: %v\n%s", err, body)
	}
}

func TestDarwinEnabledReflectsThePlistsPresence(t *testing.T) {
	home := fakeHomeDir(t)
	if enabled, err := darwinEnabled(home); err != nil || enabled {
		t.Fatalf("Enabled = %v, %v before Enable was ever called; want false, nil", enabled, err)
	}
	if err := darwinEnable(home, Target{ExecPath: "/usr/local/bin/advisor"}); err != nil {
		t.Fatal(err)
	}
	if enabled, err := darwinEnabled(home); err != nil || !enabled {
		t.Fatalf("Enabled = %v, %v after Enable; want true, nil", enabled, err)
	}
	if err := darwinDisable(home); err != nil {
		t.Fatal(err)
	}
	if enabled, err := darwinEnabled(home); err != nil || enabled {
		t.Fatalf("Enabled = %v, %v after Disable; want false, nil", enabled, err)
	}
}

func TestDarwinDisableWithoutEverEnablingIsNotAnError(t *testing.T) {
	home := fakeHomeDir(t)
	if err := darwinDisable(home); err != nil {
		t.Fatalf("darwinDisable on a machine that never enabled it: %v, want nil", err)
	}
}
