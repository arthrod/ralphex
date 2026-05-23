//go:build windows

package agentbus

import "os"

// lockFile is a no-op on Windows (advisory file locking is not implemented here).
func lockFile(_ *os.File) error { return nil }

// unlockFile is a no-op on Windows.
func unlockFile(_ *os.File) error { return nil }
