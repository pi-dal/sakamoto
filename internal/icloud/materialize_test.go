package icloud

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

func TestDatalessConfDefersRuleGraphWhileNodesSyncAndRetry(t *testing.T) {
	local, cloud := t.TempDir(), t.TempDir()
	main := "[General]\ninclude = child.conf\n[Rule]\nFINAL,PROXY\n"
	child := "[Rule]\nDOMAIN,example.com,DIRECT\n"
	write := func(root, name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(local, "main.conf", main)
	write(local, "child.conf", child)
	write(local, "nodes.txt", "old nodes")
	cfg := config.Default()
	cfg.ConfPath = filepath.Join(local, "main.conf")
	cfg.ICloud.Enabled = true
	cfg.ICloud.Directory = cloud
	cfg.ICloud.Files = []string{"nodes.txt"}
	cfg.ICloud.IncludeConf = true
	if _, err := Sync(local, cfg); err != nil {
		t.Fatal(err)
	}
	write(cloud, "nodes.txt", "new nodes")
	write(local, "main.conf", main+"# local edit\n")
	reader := func(path string) ([]byte, bool, error) {
		if path == filepath.Join(cloud, "child.conf") {
			return nil, true, syscall.EDEADLK
		}
		return read(path)
	}
	messages, err := syncSourcesWithRead(local, cfg, "", reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(messages, " "), "deferred") {
		t.Fatalf("no pending-download status: %v", messages)
	}
	if b, _ := os.ReadFile(filepath.Join(local, "nodes.txt")); string(b) != "new nodes" {
		t.Fatal("unrelated nodes were blocked")
	}
	if b, _ := os.ReadFile(filepath.Join(cloud, "main.conf")); string(b) != main {
		t.Fatal("partial rule graph was uploaded")
	}
	var baseline state
	raw, _ := os.ReadFile(filepath.Join(local, "icloud-state.json"))
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatal(err)
	}
	if baseline["main.conf"] != digest([]byte(main)) || baseline["child.conf"] != digest([]byte(child)) {
		t.Fatal("pending rule baseline advanced")
	}
	if _, err := Sync(local, cfg); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(cloud, "main.conf")); string(b) != main+"# local edit\n" {
		t.Fatal("materialized graph did not retry")
	}
}
