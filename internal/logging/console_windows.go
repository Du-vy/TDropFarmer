//go:build windows

package logging

import (
	"os"
	"sync"
	"syscall"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
	initVTOnce         sync.Once
	vtSupported        bool
)

const (
	enableVirtualTerminalProcessing = 0x0004
)

// enableVTProcessing tries to enable virtual terminal processing on standard output / console.
func enableVTProcessing() bool {
	initVTOnce.Do(func() {
		// Try stdout handle directly if it's a console
		stdoutHandle := syscall.Handle(os.Stdout.Fd())
		var mode uint32
		if err := syscall.GetConsoleMode(stdoutHandle, &mode); err == nil {
			if mode&enableVirtualTerminalProcessing != 0 {
				vtSupported = true
				return
			}
			r, _, _ := procSetConsoleMode.Call(uintptr(stdoutHandle), uintptr(mode|enableVirtualTerminalProcessing))
			if r != 0 {
				vtSupported = true
				return
			}
		}

		// Also try CONOUT$
		h, err := syscall.CreateFile(
			syscall.StringToUTF16Ptr("CONOUT$"),
			syscall.GENERIC_READ|syscall.GENERIC_WRITE,
			syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
			nil,
			syscall.OPEN_EXISTING,
			0,
			0,
		)
		if err == nil {
			defer syscall.CloseHandle(h)
			var conMode uint32
			if err := syscall.GetConsoleMode(h, &conMode); err == nil {
				if conMode&enableVirtualTerminalProcessing != 0 {
					vtSupported = true
					return
				}
				r, _, _ := procSetConsoleMode.Call(uintptr(h), uintptr(conMode|enableVirtualTerminalProcessing))
				if r != 0 {
					vtSupported = true
					return
				}
			}
		}
	})
	return vtSupported
}

func isTerminalColorSupported(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err == nil {
		if fi.Mode().IsRegular() {
			return false
		}
		if (fi.Mode() & os.ModeCharDevice) == 0 {
			// Redirected or piped. Check if running under known color-capable terminal emulator like mintty
			term := os.Getenv("TERM")
			if term != "" && term != "dumb" {
				return true
			}
			return false
		}
	}
	return enableVTProcessing()
}

