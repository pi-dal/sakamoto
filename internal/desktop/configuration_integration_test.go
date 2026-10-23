package desktop_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/desktop"
)

// Exercise the actual sidecar writer through the desktop controller. Invalid
// drafts must not replace the last valid file or rotate API credentials.
func TestConfigurationEditingEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sakamoto.yaml")
	cfg := config.Default()
	cfg.MixedInbound.Port = 2444
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	originalSecret := cfg.API.Secret
	controller := desktop.New(path, desktop.Options{ExternalCore: true})
	draft, err := controller.Configuration()
	if err != nil {
		t.Fatal(err)
	}
	set := func(key, value string) {
		t.Helper()
		for i := range draft.Fields {
			if draft.Fields[i].Key == key {
				draft.Fields[i].Value = value
				return
			}
		}
		t.Fatalf("field %q not exposed", key)
	}
	set("chain_enabled", "off")
	set("mixed_port", "2555")
	set("check_interval", "45s")
	set("icloud_files", "nodes.txt, sources/extra.conf")
	if err := controller.SaveConfiguration(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	updated, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ChainEnabled || updated.MixedInbound.Port != 2555 || updated.CheckInterval.String() != "45s" || updated.API.Secret != originalSecret {
		t.Fatalf("valid configuration was not persisted safely: %+v", updated)
	}
	stable, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	set("mixed_port", "0")
	if err := controller.SaveConfiguration(context.Background(), draft); err == nil {
		t.Fatal("invalid port was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(stable) || string(after) == string(before) {
		t.Fatal("invalid configuration changed the active sidecar")
	}
	set("mixed_port", "2555")
	set("dns_enabled", "on")
	set("dns_service", "")
	if err := controller.SaveConfiguration(context.Background(), draft); err == nil {
		t.Fatal("invalid DNS service was accepted")
	}
	if after, err = os.ReadFile(path); err != nil || string(after) != string(stable) {
		t.Fatal("failed DNS configuration changed the active sidecar")
	}
	if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "configuration-e2e.json"), []byte("{\n  \"schema\": 1,\n  \"passed\": true,\n  \"atomicSave\": true,\n  \"invalidDraftPreservedActiveFile\": true,\n  \"apiSecretPreserved\": true\n}\n"), 0o644)
	}
}
