// Package recovery contains local recovery state. It intentionally has no
// dependency on the application configuration, native core, or network APIs.
package recovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const HoldName = "maintenance.json"
const CircuitName = "crash-recovery.json"
const MaxRetries = 3

// Held treats unreadable state as held. A watcher need not read root's private
// reason in order to respect the maintenance boundary.
func Held(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, HoldName))
	return !errors.Is(err, os.ErrNotExist)
}

func Hold(dir, reason string) error {
	path := filepath.Join(dir, HoldName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err = json.NewEncoder(f).Encode(struct {
		Reason  string    `json:"reason"`
		Created time.Time `json:"created"`
	}{reason, time.Now().UTC()}); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return SyncDir(dir)
}

func Resume(dir string) error {
	// Reset the circuit before releasing the hold. A partial failure stays held.
	state, err := LoadCircuit(dir)
	if err != nil {
		state = Circuit{}
	}
	if err := SaveCircuit(dir, Circuit{Pinned: state.Pinned}); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, HoldName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return SyncDir(dir)
}

type Circuit struct {
	Attempts     int       `json:"attempts"`
	Blocked      bool      `json:"blocked"`
	RollbackUsed bool      `json:"rollback_used"`
	Pinned       bool      `json:"pinned_core"`
	Reason       string    `json:"reason,omitempty"`
	Updated      time.Time `json:"updated"`
}

func LoadCircuit(dir string) (Circuit, error) {
	var state Circuit
	raw, err := ReadPrivate(filepath.Join(dir, CircuitName), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	if state.Attempts < 0 || state.Attempts > MaxRetries {
		return state, errors.New("invalid crash recovery budget")
	}
	return state, nil
}
func SaveCircuit(dir string, state Circuit) error {
	state.Updated = time.Now().UTC()
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return Atomic(filepath.Join(dir, CircuitName), raw, 0600)
}

func ReadPrivate(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, fmt.Errorf("unsafe private state: %s", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	actual, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, actual) {
		return nil, errors.New("private state changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != actual.Size() {
		return nil, errors.New("private state changed while reading")
	}
	return raw, nil
}

// Atomic preserves an existing user's ownership, so a privileged rollback does
// not make the sidecar or learning files unreadable to their original owner.
func Atomic(path string, raw []byte, mode os.FileMode) error { return atomic(path, raw, mode, false) }
func AtomicUser(path string, raw []byte, mode os.FileMode) error {
	return atomic(path, raw, mode, true)
}
func atomic(path string, raw []byte, mode os.FileMode, userFile bool) error {
	var old os.FileInfo
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("refusing to replace nonregular recovery file")
		}
		old = info
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if old == nil && userFile {
		if info, err := os.Stat(filepath.Dir(path)); err == nil {
			old = info
		} else {
			return err
		}
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".recovery-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	fail := func(e error) error { _ = f.Close(); return e }
	if err = f.Chmod(mode); err != nil {
		return fail(err)
	}
	if old != nil && os.Geteuid() == 0 {
		if st, ok := old.Sys().(*syscall.Stat_t); ok {
			if err = f.Chown(int(st.Uid), int(st.Gid)); err != nil {
				return fail(err)
			}
		}
	}
	if _, err = f.Write(raw); err != nil {
		return fail(err)
	}
	if err = f.Sync(); err != nil {
		return fail(err)
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return SyncDir(filepath.Dir(path))
}
func SyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
