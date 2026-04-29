//go:build darwin

package app

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// macOS TIOCSTI ioctl: pushes a single byte onto the terminal's input queue
// as if the user typed it. Same value as <sys/ttycom.h>.
const tiocsti uintptr = 0x80017472

// injectIntoTTY tries to inject `text` (followed by a carriage return) into the
// controlling input of the given pty path. Returns nil on success or an error if
// the kernel rejected the ioctl (e.g. macOS sandbox), so the caller can fall back
// to AppleScript keystrokes.
func injectIntoTTY(ttyPath string, text string) error {
	if ttyPath == "" {
		return errors.New("tty path vazio")
	}
	if !strings.HasPrefix(ttyPath, "/dev/") {
		ttyPath = "/dev/" + strings.TrimPrefix(ttyPath, "/dev/")
	}
	file, err := os.OpenFile(ttyPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()

	fd := file.Fd()
	push := func(b byte) error {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, tiocsti, uintptr(unsafe.Pointer(&b)))
		if errno != 0 {
			return errno
		}
		return nil
	}
	for _, b := range []byte(text) {
		if err := push(b); err != nil {
			return err
		}
	}
	return push('\r')
}
