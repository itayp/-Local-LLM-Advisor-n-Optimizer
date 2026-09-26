package autostart

import "strings"

// windowsRunValueName is the HKCU\...\Run value's fixed name — like
// launchAgentLabel and linuxUnitName, never derived from Target.Name.
const windowsRunValueName = "LocalLLMAdvisor"

// windowsRegistry is the seam autostart_windows.go's real implementation
// satisfies. It is declared here, not behind a build tag, so
// windowsEnabledWith/EnableWith/DisableWith — the actual decision logic —
// compile and are tested on every host, even though only Windows can ever
// supply a real windowsRegistry (golang.org/x/sys/windows/registry itself
// only builds under GOOS=windows).
type windowsRegistry interface {
	// getRunValue reads the value; ok is false when it does not exist —
	// that is not an error.
	getRunValue(name string) (value string, ok bool, err error)
	setRunValue(name, value string) error
	// deleteRunValue removes the value; deleting one that does not exist
	// is not an error (Disable is idempotent).
	deleteRunValue(name string) error
}

// windowsRunCommand is exactly what gets written to the registry value:
// the Run key holds a command line, not an argument array, so ExecPath and
// each Arg are quoted the way CreateProcess's own command-line parser
// expects and space-joined.
func windowsRunCommand(t Target) string {
	parts := make([]string, 0, len(t.Args)+1)
	parts = append(parts, quoteWindowsArg(t.ExecPath))
	for _, a := range t.Args {
		parts = append(parts, quoteWindowsArg(a))
	}
	return strings.Join(parts, " ")
}

func quoteWindowsArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func windowsEnabledWith(reg windowsRegistry) (bool, error) {
	_, ok, err := reg.getRunValue(windowsRunValueName)
	return ok, err
}

func windowsEnableWith(reg windowsRegistry, t Target) error {
	return reg.setRunValue(windowsRunValueName, windowsRunCommand(t))
}

func windowsDisableWith(reg windowsRegistry) error {
	return reg.deleteRunValue(windowsRunValueName)
}
