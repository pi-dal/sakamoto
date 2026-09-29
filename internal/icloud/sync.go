// Package icloud performs optional conflict-safe bidirectional sync of user source files.
// Generated configs, daemon state, logs, API credentials and rule sets stay local.
package icloud

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
)

type state map[string]string

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func read(path string) ([]byte, bool, error) {
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil, false, nil
	}
	return b, e == nil, e
}
func atomic(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".sakamoto-sync-*")
	if e != nil {
		return e
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if e = f.Chmod(0600); e != nil {
		_ = f.Close()
		return e
	}
	if _, e = f.Write(data); e != nil {
		_ = f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		_ = f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func safeName(name string) bool {
	return name != "" && name == filepath.Base(name) && !strings.HasPrefix(name, ".") &&
		name != "config.json" && name != "sakamoto.yaml" && name != "proxy-restore.json" && name != "auto-proxy.json" && name != "api-rotation.pending.json" &&
		!strings.HasSuffix(name, ".srs") && !strings.HasSuffix(name, ".log")
}
func Sync(localDir string, cfg *config.Config) ([]string, error) {
	if !cfg.ICloud.Enabled {
		return nil, nil
	}
	remoteDir := cfg.ICloud.Directory
	if remoteDir == "" {
		return nil, fmt.Errorf("set icloud.directory")
	}
	if len(cfg.ICloud.Files) == 0 {
		return nil, fmt.Errorf("set icloud.files")
	}
	if e := os.MkdirAll(remoteDir, 0700); e != nil {
		return nil, e
	}
	statePath := filepath.Join(localDir, "icloud-state.json")
	current := state{}
	if b, exists, e := read(statePath); e != nil {
		return nil, e
	} else if exists {
		if e = json.Unmarshal(b, &current); e != nil {
			return nil, e
		}
	}
	var updates []string
	for _, name := range cfg.ICloud.Files {
		if !safeName(name) {
			return updates, fmt.Errorf("sync of %q is forbidden (only source filenames; never generated state or keys)", name)
		}
		local, remote := filepath.Join(localDir, name), filepath.Join(remoteDir, name)
		lb, le, e := read(local)
		if e != nil {
			return updates, e
		}
		rb, re, e := read(remote)
		if e != nil {
			return updates, e
		}
		if !le && !re {
			continue
		}
		if !le && re {
			if _, known := current[name]; known {
				return updates, fmt.Errorf("%s was deleted locally; no automatic overwrite, resolve manually", name)
			}
			if e = atomic(local, rb); e != nil {
				return updates, e
			}
			updates = append(updates, "downloaded from iCloud: "+name)
			current[name] = digest(rb)
			continue
		}
		if le && !re {
			if _, known := current[name]; known {
				return updates, fmt.Errorf("%s was deleted in iCloud; no automatic overwrite, resolve manually", name)
			}
			if e = atomic(remote, lb); e != nil {
				return updates, e
			}
			updates = append(updates, "uploaded: "+name)
			current[name] = digest(lb)
			continue
		}
		ld, rd := digest(lb), digest(rb)
		if ld == rd {
			current[name] = ld
			continue
		}
		previous, known := current[name]
		if !known || (previous != ld && previous != rd) {
			return updates, fmt.Errorf("%s changed on both sides; neither copy was overwritten, merge manually", name)
		}
		if previous == ld {
			if e = atomic(local, rb); e != nil {
				return updates, e
			}
			updates = append(updates, "updated from iCloud: "+name)
			current[name] = rd
		} else {
			if e = atomic(remote, lb); e != nil {
				return updates, e
			}
			updates = append(updates, "uploaded update: "+name)
			current[name] = ld
		}
	}
	b, e := json.MarshalIndent(current, "", "  ")
	if e != nil {
		return updates, e
	}
	if e = atomic(statePath, b); e != nil {
		return updates, e
	}
	return updates, nil
}
