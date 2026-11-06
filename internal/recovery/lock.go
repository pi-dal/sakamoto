package recovery

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
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
// The platform implementation uses an advisory file lock where available.
func LockRules(dir string) (func(), error) { return lockRules(dir) }
