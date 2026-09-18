//go:build linux || darwin

package hardware

import (
	"os/exec"
	"syscall"
)

// diskFree is the space available to an unprivileged user on the volume
// holding path (statfs f_bavail × f_bsize). On APFS this excludes purgeable
// space that Finder counts as available, so it can read lower than Finder —
// the honest direction to be wrong in.
func diskFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

func hideWindow(*exec.Cmd) {}
