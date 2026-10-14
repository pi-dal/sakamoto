package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutomaticGroupExclusionsValidateBeforeSaving(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sakamoto.yaml")
	cfg := Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []map[string][]string{
		{"typo": {"R2"}}, {"RealityAuto": {""}}, {"OthersAuto": {"a\nb"}}, {"RealityAuto": {"R2", "R2"}},
	} {
		cfg.AutomaticGroupExclusions = excluded
		if err := cfg.Save(path); err == nil {
			t.Fatalf("invalid exclusions saved: %v", excluded)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before) {
			t.Fatal("invalid settings changed the saved file")
		}
	}
}
