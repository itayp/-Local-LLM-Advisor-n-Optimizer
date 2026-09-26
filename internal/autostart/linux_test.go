package autostart

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCmdRunner records every call and answers from a canned map keyed by
// the joined command line, so a test never shells out to a real systemctl
// (which this environment may not even have running as a user service
// manager).
type fakeCmdRunner struct {
	calls   [][]string
	answers map[string]string // "name arg1 arg2" -> output
	fail    map[string]bool   // same key -> return an error
}

func (f *fakeCmdRunner) run(_ context.Context, name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.fail[key] {
		return f.answers[key], &fakeExecError{key}
	}
	return f.answers[key], nil
}

type fakeExecError struct{ cmd string }

func (e *fakeExecError) Error() string { return "fake: " + e.cmd + " failed" }

func TestLinuxEnableWritesTheUnitAndCallsSystemctl(t *testing.T) {
	home := fakeHomeDir(t)
	run := &fakeCmdRunner{answers: map[string]string{}}
	target := Target{Name: "Local LLM Advisor", ExecPath: "/opt/local-llm-advisor/advisor", Args: []string{"-tray"}}

	if err := linuxEnable(context.Background(), home, run, target); err != nil {
		t.Fatalf("linuxEnable: %v", err)
	}

	dir, _ := home()
	body, err := os.ReadFile(filepath.Join(dir, ".config", "systemd", "user", linuxUnitName))
	if err != nil {
		t.Fatalf("reading the unit: %v", err)
	}
	for _, want := range []string{
		"Description=Local LLM Advisor",
		"ExecStart=/opt/local-llm-advisor/advisor -tray",
		"WantedBy=default.target",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("unit is missing %q:\n%s", want, body)
		}
	}

	wantCalls := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "--now", linuxUnitName},
	}
	if len(run.calls) != len(wantCalls) {
		t.Fatalf("calls = %v, want %v", run.calls, wantCalls)
	}
	for i := range wantCalls {
		if strings.Join(run.calls[i], " ") != strings.Join(wantCalls[i], " ") {
			t.Errorf("call %d = %v, want %v", i, run.calls[i], wantCalls[i])
		}
	}
}

func TestLinuxEnableQuotesAnExecPathWithSpaces(t *testing.T) {
	home := fakeHomeDir(t)
	run := &fakeCmdRunner{answers: map[string]string{}}
	target := Target{ExecPath: "/opt/Local LLM Advisor/advisor", Args: []string{"-tray"}}
	if err := linuxEnable(context.Background(), home, run, target); err != nil {
		t.Fatalf("linuxEnable: %v", err)
	}
	dir, _ := home()
	body, err := os.ReadFile(filepath.Join(dir, ".config", "systemd", "user", linuxUnitName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `ExecStart="/opt/Local LLM Advisor/advisor" -tray`) {
		t.Errorf("ExecPath with spaces was not quoted:\n%s", body)
	}
}

func TestLinuxEnableSurfacesASystemctlFailure(t *testing.T) {
	home := fakeHomeDir(t)
	run := &fakeCmdRunner{
		answers: map[string]string{"systemctl --user daemon-reload": "Failed to reload: no systemd session"},
		fail:    map[string]bool{"systemctl --user daemon-reload": true},
	}
	err := linuxEnable(context.Background(), home, run, Target{ExecPath: "/usr/bin/advisor"})
	if err == nil {
		t.Fatal("linuxEnable = nil, want an error when systemctl fails")
	}
	if !strings.Contains(err.Error(), "no systemd session") {
		t.Errorf("error %q does not carry systemctl's own message", err)
	}
}

func TestLinuxEnabledAsksSystemctl(t *testing.T) {
	run := &fakeCmdRunner{answers: map[string]string{"systemctl --user is-enabled " + linuxUnitName: "enabled\n"}}
	enabled, err := linuxEnabled(context.Background(), run)
	if err != nil || !enabled {
		t.Fatalf("linuxEnabled = %v, %v; want true, nil", enabled, err)
	}
}

func TestLinuxEnabledDefaultsToFalseWhenSystemctlCannotAnswer(t *testing.T) {
	run := &fakeCmdRunner{
		answers: map[string]string{"systemctl --user is-enabled " + linuxUnitName: "Failed to get unit file state: No such file or directory\n"},
		fail:    map[string]bool{"systemctl --user is-enabled " + linuxUnitName: true},
	}
	enabled, err := linuxEnabled(context.Background(), run)
	if err != nil {
		t.Fatalf("linuxEnabled returned an error, want nil (a checkbox default, not a failure): %v", err)
	}
	if enabled {
		t.Errorf("linuxEnabled = true, want false")
	}
}

func TestLinuxDisableCallsSystemctl(t *testing.T) {
	run := &fakeCmdRunner{answers: map[string]string{}}
	if err := linuxDisable(context.Background(), run); err != nil {
		t.Fatalf("linuxDisable: %v", err)
	}
	want := []string{"systemctl", "--user", "disable", "--now", linuxUnitName}
	if len(run.calls) != 1 || strings.Join(run.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("calls = %v, want [%v]", run.calls, want)
	}
}
