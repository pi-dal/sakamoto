package restorepoint

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pi-dal/sakamoto/internal/recovery"
)

// BackupCandidate keeps the rejected source files before a rollback replaces
// them. These files can contain credentials and never leave the private runtime.
func BackupCandidate(dir string) (string, error) {
	base := filepath.Join(dir, "recovery")
	if err := privateDir(base); err != nil {
		return "", err
	}
	base = filepath.Join(base, "failed-candidates")
	if err := privateDir(base); err != nil {
		return "", err
	}
	folder, err := os.MkdirTemp(base, "candidate-")
	if err != nil {
		return "", err
	}
	for _, name := range []string{"config.json", "sakamoto.yaml", "auto-proxy.json", liveRules} {
		raw, err := read(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err = recovery.Atomic(filepath.Join(folder, name), raw, 0600); err != nil {
			return "", err
		}
	}
	return folder, nil
}

// Keep two stable bundles plus anything still referenced by the active/staged
// runtime or held executable. Do not accumulate an engine copy per reconnect.
func (c *Candidate) Prune(point *Point) error {
	base := filepath.Dir(point.Dir)
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	type entry struct {
		path string
		info os.FileInfo
	}
	bundles := []entry{}
	for _, item := range entries {
		if !item.IsDir() || !strings.HasPrefix(item.Name(), "runtime-") {
			continue
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		bundles = append(bundles, entry{filepath.Join(base, item.Name()), info})
	}
	sort.Slice(bundles, func(i, j int) bool { return bundles[i].info.ModTime().After(bundles[j].info.ModTime()) })
	current, err := read(filepath.Join(c.dir, "config.json"))
	if err != nil {
		return err
	}
	for i, item := range bundles {
		if i < 2 || item.path == point.Dir || strings.Contains(string(current), item.path) || strings.HasPrefix(c.engine.Name(), item.path+string(filepath.Separator)) {
			continue
		}
		referenced := false
		for _, input := range c.inputs {
			if strings.HasPrefix(input.Path, item.path+string(filepath.Separator)) {
				referenced = true
				break
			}
		}
		if referenced {
			continue
		}
		if err := privateDir(item.path); err != nil {
			return err
		}
		if err := os.RemoveAll(item.path); err != nil {
			return err
		}
	}
	return nil
}
