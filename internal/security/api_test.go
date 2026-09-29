package security

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceSecretKeepsLoopbackOnlyAndDisablesDashboard(t *testing.T) {
	old := []byte(`{"services":[{"type":"api","listen":"127.0.0.1","listen_port":9090,"secret":"change-me","dashboard":{"enabled":true}}],"route":{"final":"direct"}}`)
	newSecret := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	updated, err := replaceSecret(old, "change-me", newSecret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(updated, []byte("change-me")) {
		t.Fatal("old secret remains")
	}
	if previousSafe(old) || !previousSafe(updated) {
		t.Fatal("weak old API must never be rolled back")
	}
	var cfg map[string]any
	if err := json.Unmarshal(updated, &cfg); err != nil {
		t.Fatal(err)
	}
	api := cfg["services"].([]any)[0].(map[string]any)
	if api["secret"] != newSecret || api["listen"] != "127.0.0.1" || api["dashboard"].(map[string]any)["enabled"] != false || cfg["route"].(map[string]any)["final"] != "direct" {
		t.Fatal("rotation changed routing or failed to disable dashboard")
	}
	strongOld := []byte(`{"services":[{"type":"api","listen":"127.0.0.1","secret":"0123456789abcdef0123456789abcdef","dashboard":{"enabled":false}}]}`)
	if !previousSafe(strongOld) {
		t.Fatal("strong loopback-only previous API rejected")
	}
	for _, broken := range []string{`{"services":[{"type":"api","listen":"0.0.0.0","secret":"change-me"}]}`, `{"services":[{"type":"api","listen":"127.0.0.1","secret":"other"}]}`} {
		if _, err := replaceSecret([]byte(broken), "change-me", newSecret); err == nil {
			t.Fatal("unsafe or mismatched API service accepted")
		}
	}
}

func TestAtomicFilePrivateMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sakamoto.yaml")
	if err := atomicFile(p, []byte("api:\n  secret: private\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
}
