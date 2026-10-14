package icloud

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/pkg/sourcesync"
)

func TestGroupPolicySyncProjectsAdoptsPersistsClearsAndRejectsConflicts(t *testing.T) {
	local := t.TempDir()
	cloud := t.TempDir()
	cfg := config.Default()
	cfg.ConfPath = ""
	cfg.ICloud.Enabled = true
	cfg.ICloud.Directory = cloud
	cfg.AutomaticGroupExclusions = map[string][]string{"OthersAuto": {"B"}}
	cfgPath := filepath.Join(local, "custom.yaml")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncConfig(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(cloud, sourcesync.GroupsFile)
	if raw, err := os.ReadFile(remote); err != nil || string(raw) != `{"OthersAuto":["B"]}` {
		t.Fatal("group source not uploaded", string(raw), err)
	}
	if err := os.WriteFile(remote, []byte(`{"RealityAuto":["absent"],"OthersAuto":["A"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncConfig(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.AutomaticGroupExclusions, map[string][]string{"RealityAuto": {"absent"}, "OthersAuto": {"A"}}) {
		t.Fatal("download did not update sidecar", reloaded.AutomaticGroupExclusions)
	}
	if err := os.WriteFile(remote, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncConfig(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncConfig(cfgPath, cfg); err != nil {
		t.Fatal("clear lost at next pass", err)
	}
	reloaded, err = config.Load(cfgPath)
	if err != nil || len(reloaded.AutomaticGroupExclusions) != 0 {
		t.Fatal("clear did not persist", reloaded, err)
	}
	cfg.AutomaticGroupExclusions = map[string][]string{"OthersAuto": {"local"}}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(remote, []byte(`{"OthersAuto":["remote"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncConfig(cfgPath, cfg); err == nil {
		t.Fatal("concurrent edits overwritten")
	}
	if raw, _ := os.ReadFile(remote); string(raw) != `{"OthersAuto":["remote"]}` {
		t.Fatal("cloud overwritten")
	}
}

func TestInvalidGroupDownloadDoesNotCopyOtherSourcesOrAcknowledgeBaseline(t *testing.T) {
	local := t.TempDir()
	cloud := t.TempDir()
	cfg := config.Default()
	cfg.ConfPath = ""
	cfg.ICloud.Enabled = true
	cfg.ICloud.Directory = cloud
	if err := os.WriteFile(filepath.Join(cloud, "nodes.txt"), []byte("remote nodes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloud, sourcesync.GroupsFile), []byte(`{"Wrong":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(local, cfg); err == nil {
		t.Fatal("invalid groups accepted")
	}
	if _, err := os.Stat(filepath.Join(local, "nodes.txt")); !os.IsNotExist(err) {
		t.Fatal("other source adopted before validation")
	}
	if _, err := os.Stat(filepath.Join(local, "icloud-state.json")); !os.IsNotExist(err) {
		t.Fatal("invalid baseline acknowledged")
	}
	if err := os.WriteFile(filepath.Join(cloud, sourcesync.GroupsFile), []byte(`{"OthersAuto":["B"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(local, cfg); err != nil {
		t.Fatal(err)
	}
	var base map[string]string
	raw, _ := os.ReadFile(filepath.Join(local, "icloud-state.json"))
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	if base[sourcesync.GroupsFile] == "" || !reflect.DeepEqual(cfg.AutomaticGroupExclusions["OthersAuto"], []string{"B"}) {
		t.Fatal("groups not adopted", base, cfg.AutomaticGroupExclusions)
	}
}
