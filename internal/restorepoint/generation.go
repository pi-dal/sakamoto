package restorepoint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Materialize writes a private input generation to a trusted parent directory.
// The caller owns the result until the child using it has exited.
func (c *Candidate) Materialize(parent, stateParent string) (dir string, err error) {
	folder, err := os.MkdirTemp(parent, "sakamoto-core-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(folder)
		}
	}()
	paths := map[string]string{}
	for i, item := range c.inputs {
		path := filepath.Join(folder, fmt.Sprintf("asset-%d%s", i, filepath.Ext(item.Path)))
		if err = os.WriteFile(path, item.Data, 0600); err != nil {
			return "", err
		}
		paths[item.Path] = path
	}
	var config any
	decoder := json.NewDecoder(bytes.NewReader(c.raw))
	decoder.UseNumber()
	if err = decoder.Decode(&config); err != nil {
		return "", err
	}
	if err = walkPaths(config, func(path string) (string, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.dir, path)
		}
		replacement, ok := paths[filepath.Clean(path)]
		if !ok {
			return "", errors.New("uncaptured input")
		}
		return replacement, nil
	}); err != nil {
		return "", err
	}
	// Keep mutable mode state outside both the user-managed runtime and the
	// short-lived generation. Different workspaces get separate private caches.
	root, _ := config.(map[string]any)
	experimental, _ := root["experimental"].(map[string]any)
	cache, _ := experimental["cache_file"].(map[string]any)
	if cache["enabled"] == true {
		if !filepath.IsAbs(stateParent) {
			return "", errors.New("missing private state directory")
		}
		state := filepath.Join(stateParent, "sakamoto-state-"+digest([]byte(filepath.Clean(c.dir))))
		if err := privateDir(state); err != nil {
			return "", err
		}
		cache["path"] = filepath.Join(state, "core.db")
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(folder, "config.json"), raw, 0600); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(folder, "sakamoto.yaml"), c.sidecar, 0600); err != nil {
		return "", err
	}
	return folder, nil
}
