package restorepoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercises real private files through capture, replacement/removal, restore
// validation, promotion and tampering; no mock filesystem or online inputs.
func TestMobileRecoveryRetainedProfileOwnsAssets(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "rules.json")
	if err := os.WriteFile(source, []byte(`{"version":3,"rules":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	content := `{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"rule_set":[{"type":"local","tag":"local","format":"source","path":"` + source + `"}]}}`
	candidate, err := CaptureMobile(root, content, `{"profileID":"fixture"}`)
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Close()
	if err = os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err = candidate.Promote(); err == nil {
		t.Fatal("promoted assets that changed before stability")
	}
	if err = os.WriteFile(source, []byte(`{"version":3,"rules":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = candidate.Promote(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(source); err != nil {
		t.Fatal(err)
	}
	retained, metadata, err := LoadMobile(root)
	if err != nil {
		t.Fatal(err)
	}
	if metadata != `{"profileID":"fixture"}` || strings.Contains(retained, source) {
		t.Fatal("snapshot still relies on removed source")
	}
	var config map[string]any
	if err = json.Unmarshal([]byte(retained), &config); err != nil {
		t.Fatal(err)
	}
	path := config["route"].(map[string]any)["rule_set"].([]any)[0].(map[string]any)["path"].(string)
	if _, err = os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = LoadMobile(root); err == nil {
		t.Fatal("corrupt retained asset accepted")
	}
	evidence := map[string]any{"scenario": "mobile-configuration-snapshot", "retained_assets": true, "changed_inputs_rejected": true, "tampering_rejected": true, "engine_rollback": false}
	base := os.Getenv("SAKAMOTO_RECOVERY_ARTIFACT_DIR")
	if base == "" {
		base = t.TempDir()
	}
	if err = os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(evidence, "", "  ")
	if err = os.WriteFile(filepath.Join(base, "mobile-retained-profile.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
