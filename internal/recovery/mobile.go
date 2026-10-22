package recovery

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const MobileJournalName = "mobile-runtime.json"
const StableInterval = 5 * time.Minute

// MobileState is public diagnostic evidence, never configuration or credentials.
// Instance is a per-service identity, not an app installation/version label.
type MobileState struct {
	Schema       int       `json:"schema"`
	Phase        string    `json:"phase"`
	Token        string    `json:"token,omitempty"`
	Attempts     int       `json:"attempts"`
	Blocked      bool      `json:"blocked"`
	RollbackUsed bool      `json:"rollback_used"`
	Held         bool      `json:"held"`
	Stable       bool      `json:"stable"`
	Instance     string    `json:"instance,omitempty"`
	CoreVersion  string    `json:"core_version,omitempty"`
	Started      time.Time `json:"started,omitempty"`
	RetryAt      time.Time `json:"retry_at,omitempty"`
}

type Mobile struct {
	dir string
	now func() time.Time
}

func NewMobile(dir string) (*Mobile, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("unsafe mobile recovery directory")
	}
	return &Mobile{dir: dir, now: time.Now}, nil
}
func (m *Mobile) load() (MobileState, error) {
	state := MobileState{Schema: 1, Phase: "stopped"}
	raw, err := ReadPrivate(filepath.Join(m.dir, MobileJournalName), 8192)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, errors.New("mobile recovery journal is unreadable")
	}
	if err = json.Unmarshal(raw, &state); err != nil {
		return state, errors.New("mobile recovery journal is corrupt")
	}
	if state.Schema != 1 || state.Attempts < 0 || state.Attempts > MaxRetries {
		return state, errors.New("unsupported mobile recovery journal")
	}
	switch state.Phase {
	case "starting", "running", "failed", "stopped":
	default:
		return state, errors.New("invalid mobile recovery phase")
	}
	if (state.Phase == "starting" || state.Phase == "running") && state.Token == "" {
		return state, errors.New("missing mobile recovery identity")
	}
	return state, nil
}
func (m *Mobile) save(state MobileState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return Atomic(filepath.Join(m.dir, MobileJournalName), raw, 0600)
}

// ReadMobileState projects an atomically written journal without creating a
// directory, lock file or session. It is safe for app-side read-only diagnosis.
func ReadMobileState(dir string) (MobileState, error) {
	return (&Mobile{dir: dir, now: time.Now}).load()
}
func (m *Mobile) Status() (MobileState, error) {
	unlock, err := LockRules(m.dir)
	if err != nil {
		return MobileState{}, err
	}
	defer unlock()
	return m.load()
}
func (m *Mobile) fail(state *MobileState) {
	state.Phase = "failed"
	state.Stable = false
	state.Instance = ""
	state.Blocked = state.RollbackUsed || state.Attempts >= MaxRetries
	if !state.Blocked {
		state.RetryAt = m.now().Add(time.Duration(2<<state.Attempts) * time.Second)
	}
}

// Begin is called only after platform control confirms no current service.
// A live predecessor marker means an unclean process exit, including OOM or a
// native fatal signal. Admission is persisted before entering native code.
func (m *Mobile) Begin(manual bool) (string, error) {
	unlock, err := LockRules(m.dir)
	if err != nil {
		return "", err
	}
	defer unlock()
	state, err := m.load()
	if err != nil {
		if !manual {
			return "", err
		}
		// Explicit repair can recover a corrupt regular journal. Preserve its
		// private bytes first; unreadable/unsafe files still fail closed.
		raw, readErr := ReadPrivate(filepath.Join(m.dir, MobileJournalName), 8192)
		if readErr != nil {
			return "", errors.New("recovery journal cannot be safely reset")
		}
		var evidence [16]byte
		if _, readErr = rand.Read(evidence[:]); readErr != nil {
			return "", readErr
		}
		if readErr = Atomic(filepath.Join(m.dir, "mobile-rejected-"+hex.EncodeToString(evidence[:])+".json"), raw, 0600); readErr != nil {
			return "", readErr
		}
		state = MobileState{Schema: 1, Phase: "stopped"}
	}
	if manual {
		state = MobileState{Schema: 1, Phase: "stopped"}
	} else {
		if state.Held {
			return "", errors.New("recovery paused after manual disconnect; connect explicitly")
		}
		if state.Phase == "starting" || state.Phase == "running" {
			m.fail(&state)
			if err = m.save(state); err != nil {
				return "", err
			}
		}
		if state.Blocked {
			return "", errors.New("core recovery blocked after three retries; repair explicitly")
		}
		if state.Phase == "failed" {
			if m.now().Before(state.RetryAt) {
				return "", errors.New("core recovery is in backoff; retry after the reported deadline")
			}
			state.Attempts++
		}
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return "", err
	}
	state.Token = hex.EncodeToString(nonce[:])
	state.Phase = "starting"
	state.Stable = false
	state.Instance = ""
	state.CoreVersion = ""
	state.RetryAt = time.Time{}
	state.Started = time.Time{}
	return state.Token, m.save(state)
}

// BeginFallback admits at most one retained-profile attempt per episode.
// It never changes installed engine code. A failure blocks further automatic starts.
func (m *Mobile) BeginFallback() (string, error) {
	unlock, err := LockRules(m.dir)
	if err != nil {
		return "", err
	}
	defer unlock()
	state, err := m.load()
	if err != nil {
		return "", err
	}
	if state.Phase != "failed" || state.Held || state.RollbackUsed {
		return "", errors.New("retained profile recovery is unavailable in this episode")
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return "", err
	}
	state.Token = hex.EncodeToString(nonce[:])
	state.Phase = "starting"
	state.RollbackUsed = true
	state.Blocked = false
	state.Instance = ""
	state.RetryAt = time.Time{}
	state.Started = time.Time{}
	return state.Token, m.save(state)
}
func (m *Mobile) mutate(token string, change func(*MobileState) error) error {
	unlock, err := LockRules(m.dir)
	if err != nil {
		return err
	}
	defer unlock()
	state, err := m.load()
	if err != nil {
		return err
	}
	if token == "" || token != state.Token || state.Held || state.Phase == "stopped" {
		return errors.New("mobile recovery operation was superseded")
	}
	if err = change(&state); err != nil {
		return err
	}
	return m.save(state)
}
func (m *Mobile) Ready(token, instance, version string) error {
	return m.mutate(token, func(s *MobileState) error {
		if s.Phase != "starting" || instance == "" || version == "" {
			return errors.New("native core has not confirmed startup")
		}
		s.Phase = "running"
		s.Instance = instance
		s.CoreVersion = version
		s.Started = m.now()
		return nil
	})
}

// Replacing an applied profile restarts the stable observation interval, without
// treating an intentional reload as a crash or clearing its retry budget.
func (m *Mobile) Replaced(token, instance, version string) error {
	return m.mutate(token, func(s *MobileState) error {
		if s.Phase != "running" || instance == "" || version == "" {
			return errors.New("native replacement is unconfirmed")
		}
		s.Instance = instance
		s.CoreVersion = version
		s.Started = m.now()
		s.Stable = false
		return nil
	})
}
func (m *Mobile) Fail(token string) error {
	return m.mutate(token, func(s *MobileState) error {
		if s.Phase != "failed" {
			m.fail(s)
		}
		return nil
	})
}
func (m *Mobile) CheckStable(token, instance string) error {
	state, err := m.Status()
	if err != nil {
		return err
	}
	if state.Token != token || state.Held || state.Phase != "running" || instance == "" || instance != state.Instance || m.now().Sub(state.Started) < StableInterval {
		return errors.New("core has not retained the same verified instance for five minutes")
	}
	return nil
}
func (m *Mobile) Stable(token, instance string) error {
	return m.mutate(token, func(s *MobileState) error {
		if s.Phase != "running" || instance == "" || instance != s.Instance || m.now().Sub(s.Started) < StableInterval {
			return errors.New("core has not retained the same verified instance for five minutes")
		}
		s.Attempts = 0
		s.RollbackUsed = false
		s.Blocked = false
		s.Stable = true
		return nil
	})
}
func (m *Mobile) Stop() error {
	unlock, err := LockRules(m.dir)
	if err != nil {
		return err
	}
	defer unlock()
	// Stop remains available even if the old journal is corrupt.
	return m.save(MobileState{Schema: 1, Phase: "stopped", Held: true})
}
