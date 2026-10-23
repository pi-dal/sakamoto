package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/restorepoint"
)

type savedFile struct {
	data []byte
	mode os.FileMode
}

func snapshotGenerated(dir string) (map[string]savedFile, error) {
	result := map[string]savedFile{}
	names := []string{"config.json", "sakamoto.yaml", "auto-proxy.json", "auto-proxy-rules.json"}
	entries, err := os.ReadDir(filepath.Join(dir, "rules"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, filepath.Join("rules", entry.Name()))
		}
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
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
		result[name] = savedFile{data, info.Mode().Perm()}
	}
	return result, nil
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
func regenerate(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	return applyGeneratedConfig(cfgPath, cfg, false)
}

// Generate into a private directory and validate before touching active files.
// A successful refresh stages files only; connection remains an explicit action.
func applyGeneratedConfig(cfgPath string, cfg *config.Config, saveSource bool) error {
	if filepath.Base(cfgPath) != "sakamoto.yaml" {
		return errors.New("configuration generation expects the runtime sakamoto.yaml")
	}
	dir, err := filepath.Abs(filepath.Dir(cfgPath))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	previous, err := snapshotGenerated(dir)
	if err != nil {
		return err
	}
	token, err := recovery.RulesGeneration(dir)
	if err != nil {
		return err
	}
	candidate, err := os.MkdirTemp(dir, ".config-generation-")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(candidate)
		}
	}()
	for _, name := range []string{"config.json", "auto-proxy.json", "auto-proxy-rules.json"} {
		if file, ok := previous[name]; ok {
			if err = recovery.Atomic(filepath.Join(candidate, name), file.data, 0600); err != nil {
				return err
			}
		}
	}
	for name, file := range previous {
		if strings.HasPrefix(name, "rules/geoip-") && strings.HasSuffix(name, ".srs") {
			if err = os.MkdirAll(filepath.Join(candidate, "rules"), 0700); err != nil {
				return err
			}
			if err = recovery.Atomic(filepath.Join(candidate, name), file.data, 0600); err != nil {
				return err
			}
		}
	}
	if file, ok := previous["sakamoto.yaml"]; ok {
		if err = recovery.Atomic(filepath.Join(candidate, "sakamoto.yaml"), file.data, 0600); err != nil {
			return err
		}
	}
	if _, ok := previous["sakamoto.yaml"]; !ok || saveSource {
		err = cfg.Save(filepath.Join(candidate, "sakamoto.yaml"))
	} else {
		cfg, err = config.Load(filepath.Join(candidate, "sakamoto.yaml"))
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = gen.Run(gen.Options{Context: ctx, RequireNodes: true, ConfPath: cfg.ConfPath, SRJSONPath: cfg.SRJSONPath, NodesFile: cfg.NodesFile, AllowHosts: strings.Join(cfg.AllowHosts, ","), Cfg: cfg, OutDir: candidate, Quiet: true}); err != nil {
		return err
	}
	path := filepath.Join(candidate, "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	raw, err = gen.RebaseConfiguration(raw, candidate, dir, candidate)
	if err != nil {
		return err
	}
	if err = recovery.Atomic(path, raw, 0600); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "sing-box", "check", "-D", dir, "-c", path)
	cmd.Dir = dir
	if out, e := cmd.CombinedOutput(); e != nil {
		return fmt.Errorf("native configuration check failed: %w: %s", e, strings.TrimSpace(string(out)))
	}
	raw, err = gen.RebaseConfiguration(raw, candidate, dir, dir)
	if err != nil {
		return err
	}
	if err = recovery.Atomic(path, raw, 0600); err != nil {
		return err
	}
	manifest := restorepoint.Manifest{Files: map[string]string{"config.json": "prepared", "sakamoto.yaml": "prepared"}}
	for _, name := range []string{"auto-proxy.json", "auto-proxy-rules.json"} {
		if _, e := os.Stat(filepath.Join(candidate, name)); e == nil {
			manifest.Files[name] = "prepared"
		}
	}
	point := restorepoint.Point{Dir: candidate, Manifest: manifest}
	return point.ApplyChecked(dir, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, e := recovery.RulesGeneration(dir)
		if e != nil {
			return e
		}
		if current != token {
			return errors.New("configuration generation changed; try again")
		}
		for _, name := range []string{"config.json", "sakamoto.yaml", "auto-proxy.json", "auto-proxy-rules.json"} {
			file, existed := previous[name]
			now, e := os.ReadFile(filepath.Join(dir, name))
			if e != nil && !os.IsNotExist(e) {
				return e
			}
			if existed != (!os.IsNotExist(e)) || !bytes.Equal(now, file.data) {
				return errors.New("configuration changed during preparation; try again")
			}
		}
		// Keep checked assets even if a later file operation partially completes.
		keep = true
		return nil
	})
}
