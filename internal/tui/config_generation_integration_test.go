package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
)

func TestRegenerateChecksPrivateGenerationBeforeChangingRuntime(t *testing.T) {
	native, err := exec.LookPath("sing-box")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := config.Default()
	cfg.TailscaleOptimize = false
	cfg.ConfPath = filepath.Join(dir, "source.conf")
	cfg.NodesFile = filepath.Join(dir, "nodes.txt")
	path := filepath.Join(dir, "sakamoto.yaml")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(cfg.Save(path))
	must(os.WriteFile(cfg.NodesFile, []byte("socks://fixture:fixture@127.0.0.1:9#LocalExit\n"), 0600))
	conf := "[General]\ndns-server=https://1.1.1.1/dns-query\n[Rule]\nDOMAIN,ads.example,REJECT\nDOMAIN,proxy.example,PROXY\nDOMAIN,direct.example,DIRECT\nFINAL,DIRECT\n"
	must(os.WriteFile(cfg.ConfPath, []byte(conf), 0600))
	must(gen.Run(gen.Options{ConfPath: cfg.ConfPath, NodesFile: cfg.NodesFile, Cfg: cfg, OutDir: dir, Quiet: true}))
	before, err := snapshotGenerated(dir)
	must(err)
	must(os.WriteFile(cfg.ConfPath, []byte(strings.ReplaceAll(conf, "proxy.example", "changed.example")), 0600))
	bin := filepath.Join(dir, "bin")
	must(os.Mkdir(bin, 0700))
	entered, release := filepath.Join(dir, "entered"), filepath.Join(dir, "release")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	wrapper := "#!/bin/sh\nif [ \"$1\" = check ]; then touch " + quote(entered) + "; while [ ! -e " + quote(release) + " ]; do sleep 0.02; done; fi\nPATH=" + quote(os.Getenv("PATH")) + " exec " + quote(native) + " \"$@\"\n"
	must(os.WriteFile(filepath.Join(bin, "sing-box"), []byte(wrapper), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	result := make(chan error, 1)
	go func() { result <- regenerate(path) }()
	defer func() { _ = os.WriteFile(release, nil, 0600) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(entered); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native validation did not begin")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for name, want := range before {
		got, e := os.ReadFile(filepath.Join(dir, name))
		must(e)
		if !bytes.Equal(got, want.data) {
			t.Fatalf("generation changed active %s before check completed", name)
		}
	}
	must(os.WriteFile(release, nil, 0600))
	select {
	case e := <-result:
		must(e)
	case <-time.After(5 * time.Second):
		t.Fatal("validated generation did not complete")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	must(err)
	if bytes.Equal(raw, before["config.json"].data) {
		t.Fatal("new generation was not installed")
	}
	var output struct {
		Route struct {
			RuleSet []struct {
				Path string `json:"path"`
			} `json:"rule_set"`
		} `json:"route"`
	}
	must(json.Unmarshal(raw, &output))
	for _, rule := range output.Route.RuleSet {
		if !filepath.IsAbs(rule.Path) {
			t.Fatal("generated rule did not retain its checked asset", rule.Path)
		}
		_, e := os.Stat(rule.Path)
		must(e)
	}
	cmd := exec.Command(native, "check", "-D", dir, "-c", filepath.Join(dir, "config.json"))
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("installed generation failed native check: %v %s", e, out)
	}
	if out := os.Getenv("SAKAMOTO_RECOVERY_ARTIFACT_DIR"); out != "" {
		must(os.MkdirAll(out, 0700))
		must(os.WriteFile(filepath.Join(out, "tui-generation.json"), []byte(`{"scenario":"tui-private-generation","native_check_before_active_writes":true,"runtime_unchanged_during_check":true,"compiled_assets_retained":true,"installed_generation_passes_native_check":true,"uses_real_tun":false}`), 0600))
	}
}
