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
	"github.com/pi-dal/sakamoto/pkg/sourcesync"
)

type state map[string]string

const maxSourceBytes = 32 << 20

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func read(path string) ([]byte, bool, error) {
	if info, err := os.Lstat(path); err == nil && sourceIsDataless(info) {
		return nil, true, downloadingSource(path, errSourceDownloading)
	} else if sourceUnavailable(err) {
		return nil, true, downloadingSource(path, err)
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		if sourceUnavailable(err) {
			return nil, true, downloadingSource(path, err)
		}
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxSourceBytes+1))
	if err != nil {
		if sourceUnavailable(err) {
			return nil, true, downloadingSource(path, err)
		}
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
	return syncSources(localDir, cfg, "")
}

// SyncConfig adopts downloaded group policy into the actual sidecar before
// acknowledging its baseline. Generated configuration and secrets stay local.
func SyncConfig(cfgPath string, cfg *config.Config) ([]string, error) {
	latest, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	updates, err := syncSources(filepath.Dir(cfgPath), latest, cfgPath)
	*cfg = *latest
	return updates, err
}

func syncSources(localDir string, cfg *config.Config, cfgPath string) ([]string, error) {
	return syncSourcesWithRead(localDir, cfg, cfgPath, read)
}

func syncSourcesWithRead(localDir string, cfg *config.Config, cfgPath string, readSource func(string) ([]byte, bool, error)) ([]string, error) {
	if !cfg.ICloud.Enabled {
		return nil, nil
	}
	if !filepath.IsAbs(cfg.ICloud.Directory) {
		return nil, fmt.Errorf("set an absolute icloud.directory")
	}
	if len(cfg.ICloud.Files) == 0 && !cfg.ICloud.IncludeConf {
		return nil, fmt.Errorf("set icloud.files or enable icloud.include_conf")
	}
	pairs, err := sourcePairsWithRead(localDir, cfg, readSource)
	if err != nil {
		return nil, err
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("no local source files configured for sync")
	}
	groupsPath := filepath.Join(localDir, sourcesync.GroupsFile)
	groupsRaw, groupsExist, err := readSource(groupsPath)
	if err != nil && !sourceUnavailable(err) {
		return nil, err
	}
	if err == nil && cfg.AutomaticGroupExclusions == nil && groupsExist {
		groups, err := sourcesync.DecodeGroups(string(groupsRaw))
		if err != nil {
			return nil, err
		}
		cfg.AutomaticGroupExclusions = groups
	}
	if err == nil && (cfg.AutomaticGroupExclusions != nil || groupsExist) {
		raw, err := sourcesync.Groups(cfg.AutomaticGroupExclusions).JSON()
		if err != nil {
			return nil, err
		}
		if err := atomic(groupsPath, []byte(raw)); err != nil {
			return nil, err
		}
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
	var updates []string
	var deferredConf bool
	type sourceSnapshot struct {
		pair                      sourcePair
		local, remote             []byte
		localExists, remoteExists bool
	}
	var snapshots []sourceSnapshot
	for _, pair := range pairs {
		if pair.deferred {
			deferredConf = true
			continue
		}
		lb, le, err := readSource(pair.local)
		if sourceUnavailable(err) {
			updates = append(updates, "awaiting iCloud download: "+pair.name)
			if strings.EqualFold(filepath.Ext(pair.name), ".conf") {
				deferredConf = true
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		rb, re, err := readSource(pair.remote)
		if sourceUnavailable(err) {
			updates = append(updates, "awaiting iCloud download: "+pair.name)
			if strings.EqualFold(filepath.Ext(pair.name), ".conf") {
				deferredConf = true
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, sourceSnapshot{pair, lb, rb, le, re})
	}
	if deferredConf {
		updates = append(updates, "rule sources deferred until iCloud downloads complete")
	}
	for _, snapshot := range snapshots {
		pair, lb, rb, le, re := snapshot.pair, snapshot.local, snapshot.remote, snapshot.localExists, snapshot.remoteExists
		if deferredConf && strings.EqualFold(filepath.Ext(pair.name), ".conf") {
			continue
		}
		if !le && !re {
			continue
		}
		if pair.name == sourcesync.GroupsFile {
			if le {
				groups, err := sourcesync.DecodeGroups(string(lb))
				if err != nil {
					return nil, err
				}
				canonical, _ := groups.JSON()
				lb = []byte(canonical)
			}
			if re {
				groups, err := sourcesync.DecodeGroups(string(rb))
				if err != nil {
					return nil, err
				}
				canonical, _ := groups.JSON()
				rb = []byte(canonical)
			}
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
		if action.pair.name == sourcesync.GroupsFile && action.target == action.pair.local {
			groups, err := sourcesync.DecodeGroups(string(action.data))
			if err != nil {
				return updates, err
			}
			if cfgPath != "" {
				latest, err := config.Load(cfgPath)
				if err != nil {
					return updates, err
				}
				before, _ := sourcesync.Groups(cfg.AutomaticGroupExclusions).JSON()
				now, _ := sourcesync.Groups(latest.AutomaticGroupExclusions).JSON()
				if before != now {
					return updates, fmt.Errorf("group policy changed locally during iCloud sync; retry")
				}
				latest.AutomaticGroupExclusions = groups
				if err := latest.Save(cfgPath); err != nil {
					return updates, err
				}
			}
			cfg.AutomaticGroupExclusions = groups
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
