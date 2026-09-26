//go:build windows

package winapp

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func registerIdentity() error {
	if err := setCurrentProcessAUMID(AUMID); err != nil {
		return fmt.Errorf("winapp: setting the process AUMID: %w", err)
	}
	if err := registerDisplayName(AUMID, "Local LLM Advisor"); err != nil {
		return fmt.Errorf("winapp: registering the AUMID's display name: %w", err)
	}
	return nil
}

// setCurrentProcessAUMID calls shell32!SetCurrentProcessExplicitAppUserModelID
// directly — golang.org/x/sys/windows does not wrap this one, so it is
// resolved the same way that package resolves any other system DLL
// function: NewLazySystemDLL + NewProc, no cgo.
func setCurrentProcessAUMID(aumid string) error {
	ptr, err := windows.UTF16PtrFromString(aumid)
	if err != nil {
		return err
	}
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	proc := shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
	// The call returns an HRESULT in r1; S_OK is 0. The lastErr return is
	// GetLastError()'s value regardless of success (the usual Windows
	// syscall convention) and is only meaningful — and only reported —
	// when r1 says the call actually failed.
	r1, _, lastErr := proc.Call(uintptr(unsafe.Pointer(ptr)))
	if r1 != 0 {
		return fmt.Errorf("SetCurrentProcessExplicitAppUserModelID: hresult 0x%x: %w", r1, lastErr)
	}
	return nil
}

func registerDisplayName(aumid, displayName string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+aumid, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue("DisplayName", displayName)
}
