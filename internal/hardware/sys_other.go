//go:build !linux && !darwin && !windows

package hardware

import (
	"errors"
	"os/exec"
)

func diskFree(string) (uint64, error) {
	return 0, errors.New("free disk space is not read on this operating system")
}

func hideWindow(*exec.Cmd) {}
