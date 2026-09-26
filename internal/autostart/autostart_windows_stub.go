//go:build !windows

package autostart

// These exist only so autostart.go's dispatcher compiles on every host;
// they are never reached at runtime (runtime.GOOS is never "windows"
// here). The real implementation is autostart_windows.go.
func windowsEnabled(t Target) (bool, error) { return false, errUnsupported }
func windowsEnable(t Target) error          { return errUnsupported }
func windowsDisable() error                 { return errUnsupported }
