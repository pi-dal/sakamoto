//go:build !windows

package recovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func lockRules(dir string) (func(), error) {
	path := filepath.Join(dir, "rules-transaction.lock")
	flags := syscall.O_RDWR | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	fd, err := syscall.Open(path, flags|syscall.O_CREAT|syscall.O_EXCL, 0600)
	created := err == nil
	if errors.Is(err, syscall.EEXIST) {
		fd, err = syscall.Open(path, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	fail := func(err error) (func(), error) { _ = f.Close(); return nil, err }
	if created && os.Geteuid() == 0 {
		if err = preserveOwner(f, dir); err != nil {
			return fail(err)
		}
	}
	info, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != 0 {
		return fail(errors.New("unsafe rules transaction lock"))
	}
	deadline := time.Now().Add(time.Second)
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fail(err)
		}
		if time.Now().After(deadline) {
			return fail(fmt.Errorf("runtime rules writer remains busy: %w", err))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func preserveOwner(file *os.File, dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("runtime owner unavailable")
	}
	return file.Chown(int(st.Uid), int(st.Gid))
}

func preserveFileOwner(file *os.File, old os.FileInfo) error {
	st, ok := old.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("file owner unavailable")
	}
	return file.Chown(int(st.Uid), int(st.Gid))
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
