//go:build windows

package autostart

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// windowsRunKeyPath is HKCU, not HKLM: no admin rights needed, and it only
// ever affects the user who enabled it (product rule 5 — a click by this
// user changes this user's login, nothing system-wide).
const windowsRunKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

type realWindowsRegistry struct{}

func (realWindowsRegistry) getRunValue(name string) (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, windowsRunKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", false, fmt.Errorf("autostart: opening the Run key: %w", err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		if err == registry.ErrNotExist {
			return "", false, nil
		}
		return "", false, fmt.Errorf("autostart: reading %q: %w", name, err)
	}
	return v, true, nil
}

func (realWindowsRegistry) setRunValue(name, value string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, windowsRunKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("autostart: opening the Run key: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(name, value); err != nil {
		return fmt.Errorf("autostart: writing %q: %w", name, err)
	}
	return nil
}

func (realWindowsRegistry) deleteRunValue(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, windowsRunKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("autostart: opening the Run key: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("autostart: removing %q: %w", name, err)
	}
	return nil
}

func windowsEnabled(t Target) (bool, error) { return windowsEnabledWith(realWindowsRegistry{}) }
func windowsEnable(t Target) error          { return windowsEnableWith(realWindowsRegistry{}, t) }
func windowsDisable() error                 { return windowsDisableWith(realWindowsRegistry{}) }
