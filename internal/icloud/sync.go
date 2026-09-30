// Package icloud performs optional conflict-safe bidirectional sync of source
// files. Generated configs, daemon state, logs and API credentials stay local.
package icloud

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
)

type state map[string]string

const maxSourceBytes = 32 << 20

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func read(path string) ([]byte, bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxSourceBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxSourceBytes {
		return nil, false, fmt.Errorf("source exceeds the 32 MiB sync limit")
	}
	return data, true, nil
}
func atomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".sakamoto-sync-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

type syncAction struct {
	pair            sourcePair
	data            []byte
	target, message string
}

// Sync preflights every source/dependency before writing anything. A conflict
// or unsafe include cannot leave a partially synced rule graph behind merely
// because an unrelated nodes.txt was earlier in the source list.
func Sync(localDir string, cfg *config.Config) ([]string, error) {
	if !cfg.ICloud.Enabled {
		return nil, nil
	}
	if !filepath.IsAbs(cfg.ICloud.Directory) {
		return nil, fmt.Errorf("set an absolute icloud.directory")
	}
	if len(cfg.ICloud.Files) == 0 && !cfg.ICloud.IncludeConf {
		return nil, fmt.Errorf("set icloud.files or enable icloud.include_conf")
	}
	pairs, err := sourcePairs(localDir, cfg)
	if err != nil {
		return nil, err
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("no local source files configured for sync")
	}
	statePath := filepath.Join(localDir, "icloud-state.json")
	current := state{}
	if b, exists, err := read(statePath); err != nil {
		return nil, err
	} else if exists {
		if err := json.Unmarshal(b, &current); err != nil {
			return nil, err
		}
	}
	var actions []syncAction
	for _, pair := range pairs {
		lb, le, err := read(pair.local)
		if err != nil {
			return nil, err
		}
		rb, re, err := read(pair.remote)
		if err != nil {
			return nil, err
		}
		if !le && !re {
			continue
		}
		previous, known := current[pair.name]
		if !le && re {
			if known {
				return nil, fmt.Errorf("%s was deleted locally; no automatic overwrite, resolve manually", pair.name)
			}
			actions = append(actions, syncAction{pair, rb, pair.local, "downloaded from iCloud: " + pair.name})
			continue
		}
		if le && !re {
			if known {
				return nil, fmt.Errorf("%s was deleted in iCloud; no automatic overwrite, resolve manually", pair.name)
			}
			actions = append(actions, syncAction{pair, lb, pair.remote, "uploaded: " + pair.name})
			continue
		}
		ld, rd := digest(lb), digest(rb)
		if ld == rd {
			current[pair.name] = ld
			continue
		}
		if !known || (previous != ld && previous != rd) {
			return nil, fmt.Errorf("%s changed on both sides; neither copy was overwritten, merge manually", pair.name)
		}
		if previous == ld {
			actions = append(actions, syncAction{pair, rb, pair.local, "updated from iCloud: " + pair.name})
		} else {
			actions = append(actions, syncAction{pair, lb, pair.remote, "uploaded update: " + pair.name})
		}
	}
	var updates []string
	for _, action := range actions {
		base := cfg.ICloud.Directory
		if action.target == action.pair.local {
			base = localDir
			if rel, _ := filepath.Rel(localDir, action.target); rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				absolute, err := filepath.Abs(cfg.ConfPath)
				if err != nil {
					return updates, err
				}
				base = filepath.Dir(absolute)
			}
		}
		if err := checkPath(base, action.target); err != nil {
			return updates, err
		}
		if err := atomic(action.target, action.data); err != nil {
			return updates, err
		}
		current[action.pair.name] = digest(action.data)
		// Persist completed copies so a later I/O failure does not destroy the
		// baseline of files that have already been successfully synchronized.
		if err := saveState(statePath, current); err != nil {
			return updates, err
		}
		updates = append(updates, action.message)
	}
	if err := saveState(statePath, current); err != nil {
		return updates, err
	}
	return updates, nil
}
func saveState(path string, current state) error {
	b, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	return atomic(path, b)
}
