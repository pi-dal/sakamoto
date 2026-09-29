package security

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

func TestStageDoesNotTouchActiveFilesAndCanApplyWhenDisconnected(t *testing.T) {
	if _, err := exec.LookPath("sing-box"); err != nil {
		t.Skip("sing-box check required")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "sakamoto.yaml")
	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	core := map[string]any{
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route":     map[string]any{"final": "direct"},
		"services":  []any{map[string]any{"type": "api", "listen": "127.0.0.1", "listen_port": 19090, "secret": cfg.API.Secret, "dashboard": map[string]any{"enabled": false}}},
	}
	oldJSON, err := json.Marshal(core)
	if err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(jsonPath, oldJSON, 0600); err != nil {
		t.Fatal(err)
	}
	oldYAML, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := StageAPI(path); err != nil {
		t.Fatal(err)
	}
	pending, err := PendingAPI(path)
	if err != nil || !pending {
		t.Fatal("key not staged", err)
	}
	if raw, _ := os.ReadFile(path); !bytes.Equal(raw, oldYAML) {
		t.Fatal("staging changed live sidecar")
	}
	if raw, _ := os.ReadFile(jsonPath); !bytes.Equal(raw, oldJSON) {
		t.Fatal("staging changed live core config")
	}
	info, err := os.Stat(pendingPath(path))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("pending key not private", err)
	}
	if err := StageAPI(path); err == nil {
		t.Fatal("repeated staging silently replaced key")
	}
	key, err := readPending(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := prepare(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.install(path); err != nil {
		t.Fatal(err)
	}
	newCfg, err := config.Load(path)
	if err != nil || newCfg.API.Secret != key {
		t.Fatal("candidate not installed", err)
	}
	if err := p.restore(path); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); !bytes.Equal(raw, oldYAML) {
		t.Fatal("rollback did not restore sidecar")
	}
	if raw, _ := os.ReadFile(jsonPath); !bytes.Equal(raw, oldJSON) {
		t.Fatal("rollback did not restore core")
	}
	if err := CancelPendingAPI(path); err != nil {
		t.Fatal(err)
	}
	if pending, err := PendingAPI(path); err != nil || pending {
		t.Fatal("cancel failed", err)
	}
}
