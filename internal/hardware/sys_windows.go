//go:build windows

package hardware

import (
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// diskFree is GetDiskFreeSpaceExW's FreeBytesAvailableToCaller: what this
// user can actually write on the volume holding path (quotas included).
func diskFree(path string) (uint64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	r, _, callErr := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&avail)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&free)),
	)
	if r == 0 {
		return 0, callErr
	}
	return avail, nil
}

// createNoWindow keeps PowerShell and nvidia-smi from opening a console
// window when the daemon runs without one (step 11's tray build).
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
