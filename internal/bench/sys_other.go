//go:build !windows

package bench

import "os/exec"

// systemMemory is Windows' call; elsewhere memory is read from a file
// (/proc/meminfo) or a tool (vm_stat).
func systemMemory() (total, available uint64, ok bool) { return 0, 0, false }

func hideWindow(*exec.Cmd) {}
