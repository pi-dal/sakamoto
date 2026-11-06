//go:build windows

package recovery

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func lockRules(dir string) (func(), error) {
	path := filepath.Join(dir, "rules-transaction.lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != 0 {
		_ = f.Close()
		return nil, errors.New("unsafe rules transaction lock")
	}
	h := windows.Handle(f.Fd())
	deadline := time.Now().Add(time.Second)
	for {
		overlapped := new(windows.Overlapped)
		err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
		if err == nil {
			return func() { _ = windows.UnlockFileEx(h, 0, 1, 0, overlapped); _ = f.Close() }, nil
		}
		if err != windows.ERROR_LOCK_VIOLATION && err != windows.ERROR_IO_PENDING {
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, errors.New("runtime rules writer remains busy")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func preserveOwner(file *os.File, dir string) error          { return nil }
func preserveFileOwner(file *os.File, old os.FileInfo) error { return nil }
func syncDir(dir string) error                               { return nil }
