package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

func TestWriteTemplatePreservesUserSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sakamoto.yaml")
	original := []byte("# user-maintained sidecar\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	chain, err := writeTemplate(dir, config.Default(), []string{"ManualPick", "OthersAuto", "RealityAuto"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(chain, ",") != "RealityAuto,OthersAuto" {
		t.Fatalf("fallback ordering changed: %v", chain)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(original) {
		t.Fatalf("existing sidecar changed: %v", err)
	}
}

func TestWriteTemplateCreatesPrivateSidecar(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	if _, err := writeTemplate(dir, cfg, []string{"RealityAuto", "OthersAuto"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sakamoto.yaml")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("sidecar is not private: %v", err)
	}
	text, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(text), cfg.API.Secret) || !strings.Contains(string(text), "MainProxy: [RealityAuto, OthersAuto]") {
		t.Fatalf("template content changed: %v", err)
	}
}
