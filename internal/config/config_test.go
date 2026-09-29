package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDirOverrideAndRandomSecret(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SAKAMOTO_DIR", dir)
	if DefaultDir() != dir || DefaultPath() != filepath.Join(dir, "sakamoto.yaml") {
		t.Fatal("explicit directory override lost")
	}
	a, b := Default(), Default()
	if a.NodesFile != filepath.Join(dir, "nodes.txt") {
		t.Fatal("nodes file not in runtime directory")
	}
	if len(a.API.Secret) < 32 || a.API.Secret == b.API.Secret {
		t.Fatal("default API secret must be random")
	}
	if !a.FallbackEnabled || a.TunStack != "gvisor" {
		t.Fatal("safe defaults lost")
	}
	for _, weak := range []string{"", "change-me", "REPLACE_WITH_RANDOM_SECRET", "12345678"} {
		if ValidateAPISecret(weak) == nil {
			t.Fatalf("weak secret accepted: %q", weak)
		}
	}
	if err := ValidateAPISecret(a.API.Secret); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"https://remote.example:9090", "http://localhost:9090", "http://127.0.0.1:9090/path", "http://user@127.0.0.1:9090"} {
		a.API.URL = endpoint
		if err := a.ValidateAPIEndpoint(); err == nil {
			t.Fatalf("unsafe API endpoint accepted: %q", endpoint)
		}
	}
	a.API.URL = "http://127.0.0.1:9090"
	if err := a.ValidateAPIEndpoint(); err != nil {
		t.Fatal(err)
	}
}
func TestStaleSettingsCannotRestoreOldAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sakamoto.yaml")
	stale := Default()
	if err := stale.Save(path); err != nil {
		t.Fatal(err)
	}
	old := stale.API.Secret
	newer := Default()
	if err := os.WriteFile(path, []byte("api:\n  url: http://127.0.0.1:9090\n  secret: "+newer.API.Secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stale.LogLevel = "debug"
	if err := stale.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.API.Secret != newer.API.Secret || loaded.API.Secret == old || loaded.LogLevel != "debug" {
		t.Fatal("stale UI overwrote rotated API key", err)
	}
}

func TestLegacyProxyFinalStaysProxy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sakamoto.yaml")
	if err := os.WriteFile(path, []byte("api:\n  url: http://127.0.0.1:9090\n  secret: legacy-long-enough-random-secret-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"route":{"final":"Exit"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || cfg.Experiment.Mode != "on" {
		t.Fatalf("legacy proxy downgraded: %v %v", cfg, err)
	}
	if err := os.WriteFile(path, []byte("experiment:\n  mode: off\n  threshold: 3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil || cfg.Experiment.Mode != "off" {
		t.Fatalf("explicit off lost: %v %v", cfg, err)
	}
}
