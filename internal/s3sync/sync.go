// Package s3sync adapts the shared S3 source-bundle protocol to the host sidecar.
package s3sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/pkg/mobileconf"
	"github.com/pi-dal/sakamoto/pkg/sourcesync"
)

const CredentialsFile = "s3-credentials.json"
const StateFile = "s3-state.json"

var passMu sync.Mutex

func safePath(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			// macOS supplies these filesystem aliases; deeper source symlinks
			// still fail closed (including a symlink to a different temp dir).
			if current == "/var" || current == "/tmp" || current == "/etc" {
				continue
			}
			return errors.New("S3 sync refuses symlink paths")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return nil
}
func privateWrite(path string, raw []byte) error {
	if err := safePath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".s3-sync-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(raw); err != nil {
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
func SaveCredentials(dir string, c sourcesync.Credentials) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return privateWrite(filepath.Join(dir, CredentialsFile), raw)
}
func LoadCredentials(dir string) (sourcesync.Credentials, error) {
	var c sourcesync.Credentials
	p := filepath.Join(dir, CredentialsFile)
	if err := safePath(p); err != nil {
		return c, err
	}
	info, err := os.Lstat(p)
	if err != nil {
		return c, errors.New("configure S3 credentials in Settings first")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 64<<10 {
		return c, errors.New("S3 credentials must be a private regular file (0600)")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return c, err
	}
	if json.Unmarshal(raw, &c) != nil {
		return c, errors.New("invalid S3 credentials file")
	}
	return c, nil
}
func readSource(path string) (string, error) {
	if err := safePath(path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > sourcesync.MaxBytes {
		return "", errors.New("sync source must be a regular file below 32 MiB")
	}
	raw, err := os.ReadFile(path)
	return string(raw), err
}
func Export(cfg *config.Config) (sourcesync.Bundle, error) {
	b := sourcesync.Bundle{Version: 1, Files: map[string]string{}}
	if cfg.NodesFile != "" {
		content, err := readSource(cfg.NodesFile)
		if err != nil && !os.IsNotExist(err) {
			return b, err
		}
		if err == nil {
			b.Files["nodes.txt"] = content
		}
	}
	policy := make([]sourcesync.Policy, 0, len(cfg.PolicyRules))
	for _, p := range cfg.PolicyRules {
		policy = append(policy, sourcesync.Policy{Match: p.Match, Action: p.Action})
	}
	raw, _ := json.Marshal(policy)
	if len(policy) > 0 {
		b.Files["policy.json"] = string(raw)
	}
	subs := make([]sourcesync.Subscription, 0, len(cfg.Subscriptions))
	for _, s := range cfg.Subscriptions {
		subs = append(subs, sourcesync.Subscription{Name: s.Name, URL: s.URL, Format: s.Format})
	}
	raw, _ = json.Marshal(subs)
	if len(subs) > 0 {
		b.Files["subscriptions.json"] = string(raw)
	}
	if cfg.ConfPath != "" && !mobileconf.IsRemote(cfg.ConfPath) {
		root, err := filepath.Abs(cfg.ConfPath)
		if err != nil {
			return b, err
		}
		b.MainConf = "conf/" + filepath.Base(root)
		base := filepath.Dir(root)
		visiting := map[string]bool{}
		var visit func(string, int) error
		visit = func(file string, depth int) error {
			rel, err := filepath.Rel(base, file)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return errors.New("conf include escapes its source directory")
			}
			name := "conf/" + filepath.ToSlash(rel)
			if !sourcesync.ValidName(name) || depth > 8 || visiting[name] {
				return errors.New("invalid conf include path or cycle")
			}
			if _, ok := b.Files[name]; ok {
				return nil
			}
			content, err := readSource(file)
			if err != nil {
				return err
			}
			doc, err := mobileconf.ParseDocument(content, mobileconf.Hooks{})
			if err != nil {
				return err
			}
			visiting[name] = true
			for _, inc := range doc.Includes {
				if mobileconf.IsRemote(inc) {
					continue
				}
				if err := visit(filepath.Join(filepath.Dir(file), inc), depth+1); err != nil {
					return err
				}
			}
			delete(visiting, name)
			b.Files[name] = content
			return nil
		}
		if _, err := os.Stat(root); os.IsNotExist(err) {
			b.MainConf = ""
		} else if err := visit(root, 0); err != nil {
			return b, err
		}
	}
	return b, b.Validate()
}

// Sync saves a baseline only after every merged source is adopted. Conf files
// are staged beneath sources/s3 so remote data never overwrites arbitrary paths.
func Sync(ctx context.Context, cfgPath string) ([]string, error) {
	return syncWithClient(ctx, cfgPath, nil)
}
func syncWithClient(ctx context.Context, cfgPath string, client *http.Client) ([]string, error) {
	passMu.Lock()
	defer passMu.Unlock()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if !cfg.S3.Enabled {
		return nil, nil
	}
	dir := filepath.Dir(cfgPath)
	lockPath := filepath.Join(dir, "s3-sync.lock")
	if err := safePath(lockPath); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.New("another S3 sync is running")
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	c, err := LoadCredentials(dir)
	if err != nil {
		return nil, err
	}
	local, err := Export(cfg)
	if err != nil {
		return nil, err
	}
	var base sourcesync.Baseline
	if raw, err := os.ReadFile(filepath.Join(dir, StateFile)); err == nil {
		if json.Unmarshal(raw, &base) != nil {
			return nil, errors.New("invalid S3 sync baseline")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if _, ok := base.Hashes["policy.json"]; ok && len(cfg.PolicyRules) == 0 {
		local.Files["policy.json"] = "[]"
	}
	if _, ok := base.Hashes["subscriptions.json"]; ok && len(cfg.Subscriptions) == 0 {
		local.Files["subscriptions.json"] = "[]"
	}
	result, err := sourcesync.Sync(ctx, cfg.S3, c, local, base, client)
	if err != nil {
		return nil, err
	}
	latest, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	check, err := Export(latest)
	if err != nil {
		return nil, err
	}
	if _, ok := base.Hashes["policy.json"]; ok && len(latest.PolicyRules) == 0 {
		check.Files["policy.json"] = "[]"
	}
	if _, ok := base.Hashes["subscriptions.json"]; ok && len(latest.Subscriptions) == 0 {
		check.Files["subscriptions.json"] = "[]"
	}
	before, _ := json.Marshal(local)
	after, _ := json.Marshal(check)
	if !bytes.Equal(before, after) || !reflect.DeepEqual(latest.S3, cfg.S3) {
		return nil, errors.New("local sources changed during S3 sync; retry without overwriting")
	}
	cfg = latest // Preserve unrelated settings edited during the network request.
	writes := map[string][]byte{}
	downloads := map[string]bool{}
	for _, name := range result.Downloads {
		downloads[name] = true
	}
	if downloads["nodes.txt"] {
		target := cfg.NodesFile
		if target == "" {
			target = filepath.Join(dir, "nodes.txt")
			cfg.NodesFile = target
		}
		if info, e := os.Lstat(target); e == nil && !info.Mode().IsRegular() {
			return nil, errors.New("refusing unsafe nodes destination")
		}
		writes[target] = []byte(result.Bundle.Files["nodes.txt"])
	}
	if downloads["policy.json"] {
		var rules []sourcesync.Policy
		_ = json.Unmarshal([]byte(result.Bundle.Files["policy.json"]), &rules)
		cfg.PolicyRules = nil
		for _, r := range rules {
			cfg.PolicyRules = append(cfg.PolicyRules, config.PolicyRule{Match: r.Match, Action: r.Action})
		}
	}
	if downloads["subscriptions.json"] {
		var subs []sourcesync.Subscription
		_ = json.Unmarshal([]byte(result.Bundle.Files["subscriptions.json"]), &subs)
		cfg.Subscriptions = nil
		for _, s := range subs {
			cfg.Subscriptions = append(cfg.Subscriptions, config.SubSource{Name: s.Name, URL: s.URL, Format: s.Format})
		}
	}
	confChanged := local.MainConf != result.Bundle.MainConf
	for _, name := range result.Downloads {
		if strings.HasPrefix(name, "conf/") {
			confChanged = true
		}
	}
	if confChanged && result.Bundle.MainConf != "" {
		for name, content := range result.Bundle.Files {
			if strings.HasPrefix(name, "conf/") {
				writes[filepath.Join(dir, "sources", "s3", filepath.FromSlash(name))] = []byte(content)
			}
		}
		cfg.ConfPath = filepath.Join(dir, "sources", "s3", filepath.FromSlash(result.Bundle.MainConf))
	}
	if len(result.Downloads) > 0 || confChanged {
		raw, e := cfg.MarshalYAML()
		if e != nil {
			return nil, e
		}
		writes[cfgPath] = raw
	}
	raw, _ := json.Marshal(result.Baseline)
	writes[filepath.Join(dir, StateFile)] = raw
	if err := adoptFiles(writes, filepath.Join(dir, StateFile)); err != nil {
		return nil, err
	}
	messages := []string{}
	if result.Uploaded {
		messages = append(messages, "Source bundle uploaded to S3")
	}
	if len(result.Downloads) > 0 || confChanged {
		messages = append(messages, "Sources downloaded from S3; regenerate and reconnect to apply")
	}
	if len(messages) == 0 {
		messages = append(messages, "S3 sources are up to date")
	}
	return messages, nil
}
