package recovery

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const rulesGenerationFile = "rules-generation"

// Callers hold LockRules. Each wholesale replacement gets a nonce so that
// identical restored bytes cannot authorize an older worker (the ABA case).
func AdvanceRulesGeneration(dir string) (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	token := fmt.Sprintf("%x", nonce)
	return token, AtomicUser(filepath.Join(dir, rulesGenerationFile), []byte(token), 0600)
}
func RulesGeneration(dir string) (string, error) {
	path := filepath.Join(dir, rulesGenerationFile)
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() != 32 {
		return "", errors.New("invalid rules generation")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil {
		return "", err
	}
	if len(raw) != 32 {
		return "", errors.New("rules generation changed while reading")
	}
	return string(raw), nil
}

// LockRules serializes short file transactions, never probes or network work.
// A stopped writer cannot stall offline recovery indefinitely.
func LockRules(dir string) (func(), error) {
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
		info, err := os.Stat(dir)
		if err != nil {
			return fail(err)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fail(errors.New("runtime owner unavailable"))
		}
		if err = f.Chown(int(st.Uid), int(st.Gid)); err != nil {
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
