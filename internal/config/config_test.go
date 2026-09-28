package config

import (
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
}
