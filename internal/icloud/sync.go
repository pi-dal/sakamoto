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
		name != "config.json" && name != "sakamoto.yaml" && name != "proxy-restore.json" && name != "auto-proxy.json" &&
		!strings.HasSuffix(name, ".srs") && !strings.HasSuffix(name, ".log")
}
func Sync(localDir string, cfg *config.Config) ([]string, error) {
	if !cfg.ICloud.Enabled {
		return nil, nil
	}
	remoteDir := cfg.ICloud.Directory
	if remoteDir == "" {
		return nil, fmt.Errorf("请设置 icloud.directory")
	}
	if len(cfg.ICloud.Files) == 0 {
		return nil, fmt.Errorf("请设置 icloud.files")
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
			return updates, fmt.Errorf("不允许同步 %q（只能是源文件名，不能包含生成状态或密钥）", name)
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
				return updates, fmt.Errorf("%s 在本地被删除，未自动覆盖：请手动处理", name)
			}
			if e = atomic(local, rb); e != nil {
				return updates, e
			}
			updates = append(updates, "从 iCloud 获取 "+name)
			current[name] = digest(rb)
			continue
		}
		if le && !re {
			if _, known := current[name]; known {
				return updates, fmt.Errorf("%s 在 iCloud 被删除，未自动覆盖：请手动处理", name)
			}
			if e = atomic(remote, lb); e != nil {
				return updates, e
			}
			updates = append(updates, "上传 "+name)
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
			return updates, fmt.Errorf("%s 双端更改冲突：未覆盖任一副本，请手动合并", name)
		}
		if previous == ld {
			if e = atomic(local, rb); e != nil {
				return updates, e
			}
			updates = append(updates, "从 iCloud 更新 "+name)
			current[name] = rd
		} else {
			if e = atomic(remote, lb); e != nil {
				return updates, e
			}
			updates = append(updates, "上传更新 "+name)
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
