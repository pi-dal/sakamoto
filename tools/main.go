package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
	"github.com/pi-dal/sakamoto/pkg/mobilecore"
	"github.com/pi-dal/sakamoto/pkg/mobilegen"
	"github.com/pi-dal/sakamoto/pkg/regionaldns"
	"github.com/pi-dal/sakamoto/pkg/sourcesync"
)

func main() {
	mobile := flag.Bool("mobile-export", false, "generate for a mobile provider from captured inputs")
	checkMobile := flag.Bool("mobile-check", false, "check adapted config with the device regional DNS overlay")
	validateSources := flag.Bool("ios-sources", false, "validate iOS source snapshot on stdin")
	flag.Parse()
	if *validateSources {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, sourcesync.MaxBytes+1))
		if err != nil {
			panic(err)
		}
		if err = mobilegen.ValidateProfileSourcesJSON(string(data)); err != nil {
			panic(err)
		}
		return
	}
	if *checkMobile {
		if flag.NArg() != 2 {
			panic("expected base config and regional DNS profile")
		}
		base, err := os.ReadFile(flag.Arg(0))
		if err != nil {
			panic(err)
		}
		raw, err := os.ReadFile(flag.Arg(1))
		if err != nil {
			panic(err)
		}
		var r regionaldns.Profile
		if err = json.Unmarshal(raw, &r); err != nil {
			panic(err)
		}
		runtime, err := mobilecore.ApplyRegionalDNS(string(base), r.Domains, r.ResolverIP, r.TLSName, r.Relay, r.RelayFingerprint)
		if err != nil {
			panic(err)
		}
		if err = mobilecore.ValidateConfigJSON(runtime); err != nil {
			panic(err)
		}
		dir := filepath.Dir(flag.Arg(0))
		f, err := os.CreateTemp(dir, "checked-runtime-*.json")
		if err != nil {
			panic(err)
		}
		defer func() { _ = os.Remove(f.Name()) }()
		if _, err = f.WriteString(runtime); err != nil {
			_ = f.Close()
			panic(err)
		}
		if err = f.Close(); err != nil {
			panic(err)
		}
		check(dir, f.Name())
		return
	}
	if (!*mobile && flag.NArg() != 1) || (*mobile && flag.NArg() != 4) {
		panic("expected settings path and, for mobile export, captured conf, nodes and legacy node file paths")
	}
	cfgPath := flag.Arg(0)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		panic(err)
	}
	if *mobile {
		cfg.ConfPath, cfg.NodesFile, cfg.SRJSONPath = flag.Arg(1), flag.Arg(2), flag.Arg(3)
	}
	dir := filepath.Dir(cfgPath)
	var sourceFinal string
	var captured []gen.SRNode
	if err := gen.Run(gen.Options{CapturedSRNodes: &captured, SourceFinal: &sourceFinal, ConfPath: cfg.ConfPath, SRJSONPath: cfg.SRJSONPath, NodesFile: cfg.NodesFile,
		AllowHosts: strings.Join(cfg.AllowHosts, ","), Cfg: cfg, OutDir: dir, MobileProvider: *mobile, OmitLearningProbe: *mobile, Quiet: *mobile}); err != nil {
		panic(err)
	}
	if *mobile {
		if captured == nil {
			captured = []gen.SRNode{}
		}
		generation, err := json.Marshal(map[string]any{"nodes": captured, "fingerprint": cfg.UTLSFingerprint})
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "mobile-generation.json"), generation, 0600); err != nil {
			panic(err)
		}
		groups, err := sourcesync.Groups(cfg.AutomaticGroupExclusions).JSON()
		if err != nil {
			panic(err)
		}
		if err = os.WriteFile(filepath.Join(dir, sourcesync.GroupsFile), []byte(groups), 0600); err != nil {
			panic(err)
		}
		metadata := map[string]any{
			"sourceFinal": sourceFinal,
			"experiment": map[string]any{
				"mode": cfg.Experiment.Mode, "threshold": cfg.Experiment.Threshold,
				"cf_region_block": cfg.Experiment.CFRegionBlock,
			},
			"fallbacks": cfg.Fallbacks, "fallback_enabled": cfg.FallbackEnabled, "recover_after": cfg.RecoverAfter,
			"chain_enabled": cfg.ChainEnabled, "chain_exit": cfg.ChainExit, "socks_exit": cfg.SocksExit,
			"block_quic": cfg.BlockQUIC, "block_stun": cfg.BlockSTUN, "strict_route": cfg.StrictRoute, "tun_stack": cfg.TunStack,
		}
		data, err := json.Marshal(metadata)
		if err != nil {
			panic(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "mobile-policy.json"), data, 0600); err != nil {
			panic(err)
		}
	}
	if *mobile && cfg.DNSGuard.Enabled {
		guard := config.MobileDNSGuard{LocalDomains: append([]string{}, cfg.DNSGuard.LocalDomains...), BootstrapIP: cfg.DNSGuard.BootstrapIP, BootstrapServerName: cfg.DNSGuard.BootstrapServerName}
		data, err := json.Marshal(guard)
		if err != nil {
			panic(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "mobile-dns-guard.json"), data, 0600); err != nil {
			panic(err)
		}
	}
	check(dir, filepath.Join(dir, "config.json"))
}
func check(dir, path string) {
	// -D keeps runtime-relative rule paths (rules/*.srs) resolvable regardless
	// of the caller's cwd, matching the daemon/import directory contract.
	out, err := exec.Command("sing-box", "check", "-D", dir, "-c", path).CombinedOutput()
	if err != nil {
		panic(fmt.Errorf("sing-box check: %w: %s", err, out))
	}
	fmt.Println("generated + validated:", filepath.Join(dir, "config.json"))
}
