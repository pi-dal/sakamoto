package icloud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

func TestSyncUploadDownloadAndConflict(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "local")
	remote := filepath.Join(root, "cloud")
	if err := os.Mkdir(local, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(remote, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.ICloud.Enabled = true
	c.ConfPath = ""
	c.ICloud.Directory = remote
	c.ICloud.Files = []string{"nodes.txt"}
	src := filepath.Join(local, "nodes.txt")
	dst := filepath.Join(remote, "nodes.txt")
	if err := os.WriteFile(src, []byte("one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, e := Sync(local, c); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(dst); string(b) != "one\n" {
		t.Fatal("upload failed")
	}
	if err := os.WriteFile(dst, []byte("two\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, e := Sync(local, c); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(src); string(b) != "two\n" {
		t.Fatal("download failed")
	}
	if err := os.WriteFile(src, []byte("local\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("remote\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, e := Sync(local, c); e == nil || !strings.Contains(e.Error(), "changed on both sides") {
		t.Fatal("conflict must not overwrite either side", e)
	}
	if b, _ := os.ReadFile(src); string(b) != "local\n" {
		t.Fatal("local overwritten")
	}
	if b, _ := os.ReadFile(dst); string(b) != "remote\n" {
		t.Fatal("cloud overwritten")
	}
}
func TestSyncRejectsGeneratedAndSecretFiles(t *testing.T) {
	c := config.Default()
	c.ICloud.Enabled = true
	c.ConfPath = ""
	c.ICloud.Directory = t.TempDir()
	for _, name := range []string{"config.json", "sakamoto.yaml", "../secrets", "rules.srs", "proxy-restore.json", "auto-proxy.json", "api-rotation.pending.json", "watch.sock", "watch.lock", "dns-restore.json", "sources/dns-restore.json", "sources/config.json", "sources/Sakamoto.yaml", "nested/../notes.conf", "sources/file.srs"} {
		c.ICloud.Files = []string{name}
		if _, e := Sync(t.TempDir(), c); e == nil {
			t.Errorf("accepted forbidden %s", name)
		}
	}
}
