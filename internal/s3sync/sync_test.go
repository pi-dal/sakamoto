package s3sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/icloud"
	"github.com/pi-dal/sakamoto/pkg/sourcesync"
)

func TestCredentialsAndSourceScope(t *testing.T) {
	dir := t.TempDir()
	c := sourcesync.Credentials{AccessKey: "synthetic-access", SecretKey: "synthetic-secret"}
	if err := SaveCredentials(dir, c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, CredentialsFile))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credentials are not private", err)
	}
	loaded, err := LoadCredentials(dir)
	if err != nil || loaded != c {
		t.Fatal("credentials not restored", err)
	}
	for _, name := range []string{CredentialsFile, StateFile, "s3-sync.lock"} {
		if icloud.ValidSourceName(name) {
			t.Fatal("private S3 state allowed in iCloud", name)
		}
	}
	cfg := config.Default()
	cfg.NodesFile = filepath.Join(dir, "nodes.txt")
	cfg.ConfPath = filepath.Join(dir, "main.conf")
	if err := os.WriteFile(cfg.ConfPath, []byte("[General]\ninclude=ad.conf\n[Rule]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ad.conf"), []byte("[Rule]\nDOMAIN,ads.example,REJECT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.PolicyRules = []config.PolicyRule{{Match: "example.com", Action: "proxy"}}
	bundle, err := Export(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.MainConf != "conf/main.conf" || len(bundle.Files) != 3 {
		t.Fatal("source graph not exported", bundle.MainConf, len(bundle.Files))
	}
	if _, ok := bundle.Files["sakamoto.yaml"]; ok {
		t.Fatal("sidecar leaked")
	}
	if _, ok := bundle.Files[CredentialsFile]; ok {
		t.Fatal("S3 credentials leaked")
	}
	if err := os.Chmod(filepath.Join(dir, CredentialsFile), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(dir); err == nil {
		t.Fatal("public credentials file accepted")
	}
}
func TestFreshHostDownloadsWithoutClearingDeviceSettings(t *testing.T) {
	bundle := sourcesync.Bundle{Version: 1, MainConf: "conf/main.conf", Files: map[string]string{
		"policy.json":    `[{"match":"example.com","action":"proxy"}]`,
		"conf/main.conf": "[General]\ninclude=ad.conf\n[Rule]\n",
		"conf/ad.conf":   "[Rule]\nDOMAIN,ads.example,REJECT\n",
	}}
	raw, _ := json.Marshal(bundle)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("ETag", `"one"`); _, _ = w.Write(raw) }))
	defer server.Close()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.NodesFile = filepath.Join(dir, "missing-nodes.txt")
	cfg.ConfPath = filepath.Join(dir, "missing.conf")
	cfg.S3 = sourcesync.Settings{Enabled: true, Endpoint: server.URL, Region: "auto", Bucket: "test", Prefix: "sakamoto"}
	path := filepath.Join(dir, "sakamoto.yaml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := SaveCredentials(dir, sourcesync.Credentials{AccessKey: "example", SecretKey: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := syncWithClient(context.Background(), path, server.Client()); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.PolicyRules) != 1 || loaded.API.Secret != cfg.API.Secret || loaded.ConfPath != filepath.Join(dir, "sources", "s3", "conf", "main.conf") {
		t.Fatal("sources not adopted or device settings changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "sources", "s3", "conf", "ad.conf")); err != nil {
		t.Fatal("include not downloaded", err)
	}
	if _, err := Export(loaded); err != nil {
		t.Fatal("downloaded graph does not re-export", err)
	}
}

func TestExportRejectsSymlinkIncludes(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.conf"), []byte("[Rule]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "nested")); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.conf")
	_ = os.WriteFile(main, []byte("[General]\ninclude=nested/secret.conf\n[Rule]\n"), 0600)
	cfg := config.Default()
	cfg.NodesFile = ""
	cfg.ConfPath = main
	if _, err := Export(cfg); err == nil {
		t.Fatal("symlink escaped the source graph")
	}
}
