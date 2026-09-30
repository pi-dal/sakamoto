package icloud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

func ruleSyncConfig(t *testing.T) (string, string, *config.Config) {
	t.Helper()
	base := t.TempDir()
	local, cloud := filepath.Join(base, "local"), filepath.Join(base, "cloud")
	for _, path := range []string{local, cloud} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.ICloud.Enabled = true
	cfg.ICloud.Directory = cloud
	cfg.ICloud.Files = []string{"nodes.txt"}
	cfg.ConfPath = filepath.Join(local, "sources", "current", "macOS.conf")
	return local, cloud, cfg
}
func putSource(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func requireText(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != want {
		t.Fatalf("wrong synced source %s: %q %v", path, raw, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("synced sources must remain private", err)
	}
}
func TestSyncAutomaticallyIncludesNestedRuleGraph(t *testing.T) {
	local, cloud, cfg := ruleSyncConfig(t)
	main := "[General]\ninclude = rules/ad.conf\n[Rule]\nFINAL,DIRECT\n"
	ad := "[General]\ninclude = extra.conf\n[Rule]\nDOMAIN,ads.example,REJECT\n"
	extra := "[Rule]\nDOMAIN,app.example,PROXY\n"
	putSource(t, cfg.ConfPath, main)
	putSource(t, filepath.Join(filepath.Dir(cfg.ConfPath), "rules/ad.conf"), ad)
	putSource(t, filepath.Join(filepath.Dir(cfg.ConfPath), "rules/extra.conf"), extra)
	putSource(t, filepath.Join(local, "nodes.txt"), "synthetic-node\n")
	// "rules" here is a source include directory, not generated runtime rules.
	if _, err := Sync(local, cfg); err != nil {
		t.Fatal(err)
	}
	for rel, text := range map[string]string{"sources/current/macOS.conf": main, "sources/current/rules/ad.conf": ad, "sources/current/rules/extra.conf": extra, "nodes.txt": "synthetic-node\n"} {
		requireText(t, filepath.Join(cloud, rel), text)
	}
	newMain := "[General]\ninclude = rules/new.conf\n[Rule]\nFINAL,DIRECT\n"
	newChild := "[Rule]\nDOMAIN,new.example,PROXY\n"
	putSource(t, filepath.Join(cloud, "sources/current/macOS.conf"), newMain)
	putSource(t, filepath.Join(cloud, "sources/current/rules/new.conf"), newChild)
	if _, err := Sync(local, cfg); err != nil {
		t.Fatal(err)
	}
	requireText(t, cfg.ConfPath, newMain)
	requireText(t, filepath.Join(filepath.Dir(cfg.ConfPath), "rules/new.conf"), newChild)
}
func TestInitialDownloadDiscoversCloudOnlyIncludes(t *testing.T) {
	local, cloud, cfg := ruleSyncConfig(t)
	cfg.ICloud.Files = nil // Rules-only syncing is supported.
	main := "[General]\ninclude = ad.conf\n[Rule]\nFINAL,DIRECT\n"
	ad := "[Rule]\nDOMAIN,ads.example,REJECT\n"
	putSource(t, filepath.Join(cloud, "sources/current/macOS.conf"), main)
	putSource(t, filepath.Join(cloud, "sources/current/ad.conf"), ad)
	if _, err := Sync(local, cfg); err != nil {
		t.Fatal(err)
	}
	requireText(t, cfg.ConfPath, main)
	requireText(t, filepath.Join(filepath.Dir(cfg.ConfPath), "ad.conf"), ad)
}
func TestUnsafeOrConflictingConfDoesNotPartiallyUploadNodes(t *testing.T) {
	for _, scenario := range []string{"traversal", "generated", "conflict", "cycle", "missing", "not-conf"} {
		t.Run(scenario, func(t *testing.T) {
			local, cloud, cfg := ruleSyncConfig(t)
			putSource(t, filepath.Join(local, "nodes.txt"), "private-test-node\n")
			main := "[Rule]\nFINAL,DIRECT\n"
			switch scenario {
			case "not-conf":
				main = `{"api_secret":"synthetic-not-a-rule-file"}`
			case "traversal":
				main = "[General]\ninclude=../../../private.conf\n"
			case "generated":
				main = "[General]\ninclude=config.json\n"
			case "cycle":
				main = "[General]\ninclude=macOS.conf\n"
			case "missing":
				main = "[General]\ninclude=missing.conf\n"
			case "conflict":
				putSource(t, filepath.Join(cloud, "sources/current/macOS.conf"), "[Rule]\nFINAL,PROXY\n")
			}
			putSource(t, cfg.ConfPath, main)
			if _, err := Sync(local, cfg); err == nil {
				t.Fatal("unsafe/conflicting rule graph accepted")
			}
			if _, err := os.Stat(filepath.Join(cloud, "nodes.txt")); !os.IsNotExist(err) {
				t.Fatal("nodes uploaded before dependency preflight finished")
			}
		})
	}
}
func TestSourceSymlinksAreNeverFollowed(t *testing.T) {
	local, cloud, cfg := ruleSyncConfig(t)
	cfg.ICloud.IncludeConf = false
	outside := filepath.Join(t.TempDir(), "secret")
	putSource(t, outside, "never-upload\n")
	if err := os.Symlink(outside, filepath.Join(local, "nodes.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(local, cfg); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatal("symlink source accepted", err)
	}
	if _, err := os.Stat(filepath.Join(cloud, "nodes.txt")); !os.IsNotExist(err) {
		t.Fatal("symlink source leaked")
	}
}
func TestConfDeletionIsNotSilentlyRestored(t *testing.T) {
	local, _, cfg := ruleSyncConfig(t)
	putSource(t, cfg.ConfPath, "[Rule]\nFINAL,DIRECT\n")
	if _, err := Sync(local, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cfg.ConfPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(local, cfg); err == nil || !strings.Contains(err.Error(), "deleted locally") {
		t.Fatal("conf deletion must require manual resolution", err)
	}
	if _, err := os.Stat(cfg.ConfPath); !os.IsNotExist(err) {
		t.Fatal("deleted conf silently restored")
	}
}

func TestCloudParentSymlinkIsRejectedBeforeCopy(t *testing.T) {
	local, cloud, cfg := ruleSyncConfig(t)
	cfg.ICloud.IncludeConf = false
	cfg.ICloud.Files = []string{"sources/extra.conf"}
	putSource(t, filepath.Join(local, "sources/extra.conf"), "[Rule]\nFINAL,DIRECT\n")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(cloud, "sources")); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(local, cfg); err == nil {
		t.Fatal("cloud parent symlink accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "extra.conf")); !os.IsNotExist(err) {
		t.Fatal("copy escaped cloud root")
	}
}

func TestExternalConfMapsToPortableCloudFolder(t *testing.T) {
	local, cloud, cfg := ruleSyncConfig(t)
	cfg.ConfPath = filepath.Join(t.TempDir(), "main.conf")
	cfg.ICloud.Files = nil
	main := "[General]\ninclude=ad.conf\n[Rule]\nFINAL,DIRECT\n"
	ad := "[Rule]\nDOMAIN,ads.example,REJECT\n"
	putSource(t, cfg.ConfPath, main)
	putSource(t, filepath.Join(filepath.Dir(cfg.ConfPath), "ad.conf"), ad)
	if _, err := Sync(local, cfg); err != nil {
		t.Fatal(err)
	}
	requireText(t, filepath.Join(cloud, "conf/main.conf"), main)
	requireText(t, filepath.Join(cloud, "conf/ad.conf"), ad)
}
func TestIncludeConfCanBeDisabled(t *testing.T) {
	local, cloud, cfg := ruleSyncConfig(t)
	cfg.ICloud.IncludeConf = false
	putSource(t, cfg.ConfPath, "[Rule]\nFINAL,DIRECT\n")
	putSource(t, filepath.Join(local, "nodes.txt"), "synthetic-node\n")
	if _, err := Sync(local, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cloud, "sources/current/macOS.conf")); !os.IsNotExist(err) {
		t.Fatal("rule files synced despite opt-out")
	}
}
