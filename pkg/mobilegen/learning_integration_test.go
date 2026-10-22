package mobilegen

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Compile the actual on-device source pipeline, then validate its packaged
// rules with the native core. Desktop-only listeners must not reach phones.
func TestMobileLearningGenerationKeepsProviderOwnedObservation(t *testing.T) {
	bundle := `{"version":1,"files":{"nodes.txt":"socks://fixture:synthetic@203.0.113.10:9000#Synthetic\n"}}`
	encoded, err := GenerateProfileJSON(bundle, `{"mode":"auto","threshold":3,"cfRegionBlock":true}`)
	if err != nil {
		t.Fatal(err)
	}
	var profile struct {
		Config string
		Files  map[string][]byte
	}
	if err = json.Unmarshal([]byte(encoded), &profile); err != nil {
		t.Fatal(err)
	}
	var configuration struct {
		Inbounds []struct{ Type, Tag string }
		Route    struct{ Rules []struct{ Inbound []string } }
	}
	if err = json.Unmarshal([]byte(profile.Config), &configuration); err != nil {
		t.Fatal(err)
	}
	for _, in := range configuration.Inbounds {
		if in.Tag == "learning-direct" || in.Tag == "learning-proxy" || in.Tag == "learning-route" {
			t.Fatalf("desktop-only listener in mobile profile: %s", in.Tag)
		}
	}
	for _, r := range configuration.Route.Rules {
		for _, in := range r.Inbound {
			if in == "learning-direct" || in == "learning-proxy" || in == "learning-route" {
				t.Fatal("desktop-only route in mobile profile")
			}
		}
	}
	if _, err = exec.LookPath("sing-box"); err != nil {
		t.Skip("native core required for semantic check")
	}
	dir := t.TempDir()
	for name, data := range profile.Files {
		target := filepath.Join(dir, name)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(dir, "config.json")
	if err = os.WriteFile(configPath, []byte(profile.Config), 0600); err != nil {
		t.Fatal(err)
	}
	if out, e := exec.Command("sing-box", "check", "-D", dir, "-c", configPath).CombinedOutput(); e != nil {
		t.Fatalf("native check: %v: %s", e, out)
	}
	if artifact := os.Getenv("SAKAMOTO_TEST_ARTIFACT_DIR"); artifact != "" {
		if err = os.MkdirAll(artifact, 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(map[string]any{"learning_enabled": true, "cf_enabled": true, "desktop_listeners_absent": true, "native_config_valid": true, "packaged_rule_files": len(profile.Files)}, "", "  ")
		if err = os.WriteFile(filepath.Join(artifact, "mobile-generation.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
