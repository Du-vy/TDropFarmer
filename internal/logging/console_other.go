//go:build !windows

package logging

import (
	"os"
)

func isTerminalColorSupported(f *os.File) bool {
	if f == nil {
		return false
	}
	fileInfo, err := f.Stat()
	if err != nil {
		return false
	}
	// If redirected to a regular disk file, disable ANSI escape sequences.
	if fileInfo.Mode().IsRegular() {
		return false
	}
	// On Linux/Unix: TTYs, character devices, and pipes (Docker, Portainer,
	// systemd-journald, Kubernetes, CI runners) natively support ANSI colors.
	return true
}

