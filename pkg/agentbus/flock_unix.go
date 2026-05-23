//go:build !windows

package agentbus

import (
	"fmt"
	"os"
	"syscall"
)

// lockFile acquires an exclusive advisory lock on the file.
func lockFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	return nil
}

// unlockFile releases the lock on the file.
func unlockFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("flock unlock: %w", err)
	}
	return nil
}
