package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
)

type savedFile struct {
	data []byte
	mode os.FileMode
}

// snapshotGenerated protects the last working config when an import or check fails.
func snapshotGenerated(dir string) (map[string]savedFile, error) {
	result := map[string]savedFile{}
	for _, rel := range []string{"config.json"} {
		path := filepath.Join(dir, rel)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		result[rel] = savedFile{data, info.Mode().Perm()}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "rules"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		rel := filepath.Join("rules", entry.Name())
		path := filepath.Join(dir, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		result[rel] = savedFile{data, info.Mode().Perm()}
	}
	return result, nil
}
func restoreGenerated(dir string, saved map[string]savedFile) {
	entries, _ := os.ReadDir(filepath.Join(dir, "rules"))
	for _, entry := range entries {
		if _, ok := saved[filepath.Join("rules", entry.Name())]; !ok && !entry.IsDir() {
			_ = os.Remove(filepath.Join(dir, "rules", entry.Name()))
		}
	}
	if _, ok := saved["config.json"]; !ok {
		_ = os.Remove(filepath.Join(dir, "config.json"))
	}
	for rel, file := range saved {
		_ = os.WriteFile(filepath.Join(dir, rel), file.data, file.mode)
	}
}

func importSource(cfgPath, source string) error {
	if err := gen.ValidSource(source); err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	cfg.ConfPath = source
	return applyGeneratedConfig(cfgPath, cfg, true)
}

// regenerate validates and rolls back updates just like imports. A failed
// refresh must never leave a partially updated rules/ and config.json pair.
func regenerate(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	return applyGeneratedConfig(cfgPath, cfg, false)
}
func applyGeneratedConfig(cfgPath string, cfg *config.Config, saveSource bool) error {
	dir := filepath.Dir(cfgPath)
	previous, err := snapshotGenerated(dir)
	if err != nil {
		return err
	}
	commit := false
	defer func() {
		if !commit {
			restoreGenerated(dir, previous)
		}
	}()
	if err := gen.Run(gen.Options{ConfPath: cfg.ConfPath, SRJSONPath: cfg.SRJSONPath, NodesFile: cfg.NodesFile,
		AllowHosts: strings.Join(cfg.AllowHosts, ","), Cfg: cfg, OutDir: dir, Quiet: true}); err != nil {
		return err
	}
	cmd := exec.Command("sing-box", "check", "-c", filepath.Join(dir, "config.json"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sing-box 校验失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if saveSource {
		if err := cfg.Save(cfgPath); err != nil {
			return fmt.Errorf("转换成功但保存来源失败: %w", err)
		}
	}
	commit = true
	return nil
}
