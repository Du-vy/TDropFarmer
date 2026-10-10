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
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}
