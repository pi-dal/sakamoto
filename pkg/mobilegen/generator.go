// Package mobilegen owns filesystem/network generation separately from the
// pure mobilecore vocabulary/import-validation bridge.
package mobilegen

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
	"github.com/pi-dal/sakamoto/internal/icloud"
	"github.com/pi-dal/sakamoto/pkg/sourcesync"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// GenerateProfileJSON compiles a content-only snapshot in a private temporary
// directory. It reuses the host parser, subscriptions and in-process SRS writer;
// it never executes system setup or invokes an external sing-box binary.
// The caller performs Libbox semantic validation before committing the result.
func GenerateProfileJSON(bundleJSON, settingsJSON string) (string, error) {
	return GenerateProfileWithFetcherJSON(bundleJSON, settingsJSON, nil)
}

// GenerateProfileWithFetcherJSON uses the platform source-fetching adapter.
func GenerateProfileWithFetcherJSON(bundleJSON, settingsJSON string, fetcher SourceFetcher) (string, error) {
	if len(bundleJSON) > sourcesync.MaxBytes {
		return "", fmt.Errorf("source bundle exceeds 32 MiB")
	}
	var bundle sourcesync.Bundle
	if json.Unmarshal([]byte(bundleJSON), &bundle) != nil || bundle.Version != 1 || len(bundle.Files) > 128 {
		return "", fmt.Errorf("invalid source snapshot")
	}
	if bundle.MainConf == "" {
		for name := range bundle.Files {
			if strings.HasSuffix(strings.ToLower(name), ".conf") {
				return "", fmt.Errorf("choose a main conf before generating")
			}
		}
	}
	// nodes.txt import accepts a UTF-8 BOM; the shared host parser expects
	// plain links. Normalize only the compilation copy, preserving sync bytes.
	nodeText, hasNodes := bundle.Files["nodes.txt"]
	if hasNodes {
		bundle.Files["nodes.txt"] = strings.TrimPrefix(nodeText, "\ufeff")
	}
	// iCloud preserves runtime-relative conf names; the host generator accepts
	// them. The S3-only conf/ prefix restriction is intentionally not applied.
	check := sourcesync.Bundle{Version: 1, Files: map[string]string{}}
	for _, name := range []string{"nodes.txt", "policy.json", "subscriptions.json"} {
		if value, ok := bundle.Files[name]; ok {
			check.Files[name] = value
		}
	}
	if err := check.Validate(); err != nil {
		return "", err
	}
	// Validate includes as a closed graph before allowing the host parser to
	// open anything. Prefixing keeps the original relative topology while
	// using the shared S3 bundle's dependency/traversal checks.
	for name, body := range bundle.Files {
		if strings.HasSuffix(strings.ToLower(name), ".conf") {
			check.Files[path.Join("conf", name)] = body
		}
	}
	if bundle.MainConf != "" {
		check.MainConf = "conf/" + bundle.MainConf
	}
	if err := check.Validate(); err != nil {
		return "", err
	}
	total := 0
	for name, body := range bundle.Files {
		total += len(body)
		if !icloud.ValidSourceName(name) || total > sourcesync.MaxBytes {
			return "", fmt.Errorf("invalid source snapshot path or size")
		}
		if name != "nodes.txt" && name != "policy.json" && name != "subscriptions.json" && !strings.HasSuffix(strings.ToLower(name), ".conf") {
			return "", fmt.Errorf("unsupported source file")
		}
	}
	cfg := config.Default()
	cfg.TailscaleOptimize = false
	cfg.DNSGuard.Enabled = false
	cfg.MixedInbound.Enabled = false
	cfg.SystemProxy.Enabled = false
	cfg.SRJSONPath = ""
	cfg.AllowHosts = nil
	cfg.ChainEnabled = false
	var settings struct {
		Mode          string `json:"mode"`
		Threshold     int    `json:"threshold"`
		CFRegionBlock bool   `json:"cfRegionBlock"`
	}
	if settingsJSON != "" {
		if json.Unmarshal([]byte(settingsJSON), &settings) != nil {
			return "", fmt.Errorf("invalid Experiment settings")
		}
		cfg.Experiment.Mode = settings.Mode
		cfg.Experiment.Threshold = settings.Threshold
		cfg.Experiment.CFRegionBlock = settings.CFRegionBlock
	}
	if err := cfg.ValidateExperiment(); err != nil {
		return "", err
	}
	if raw := bundle.Files["policy.json"]; raw != "" {
		var policies []sourcesync.Policy
		_ = json.Unmarshal([]byte(raw), &policies)
		for _, p := range policies {
			cfg.PolicyRules = append(cfg.PolicyRules, config.PolicyRule{Match: p.Match, Action: p.Action})
		}
	}
	if raw := bundle.Files["subscriptions.json"]; raw != "" {
		var subs []sourcesync.Subscription
		_ = json.Unmarshal([]byte(raw), &subs)
		for _, s := range subs {
			cfg.Subscriptions = append(cfg.Subscriptions, config.SubSource{Name: s.Name, URL: s.URL, Format: s.Format})
		}
	}
	work, err := os.MkdirTemp("", "sakamoto-mobile-generate-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(work) }()
	for name, body := range bundle.Files {
		p := filepath.Join(work, "sources", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			return "", err
		}
	}
	main := bundle.MainConf
	if main == "" {
		main = "default.conf"
		if err := os.WriteFile(filepath.Join(work, "sources", main), []byte("[General]\ndns-server = 1.1.1.1\n[Rule]\nFINAL,DIRECT\n"), 0600); err != nil {
			return "", err
		}
	} else if !icloud.ValidSourceName(main) || !strings.HasSuffix(strings.ToLower(main), ".conf") || bundle.Files[main] == "" {
		return "", fmt.Errorf("main source is missing")
	}
	cfg.ConfPath = filepath.Join(work, "sources", filepath.FromSlash(main))
	cfg.NodesFile = filepath.Join(work, "sources", "nodes.txt")
	out := filepath.Join(work, "runtime")
	if err := gen.Run(gen.Options{ConfPath: cfg.ConfPath, NodesFile: cfg.NodesFile, Cfg: cfg, OutDir: out, Quiet: true, HTTPClient: sourceClient(fetcher)}); err != nil {
		return "", fmt.Errorf("generation failed: %s", sanitizeGenerationError(err))
	}
	raw, err := os.ReadFile(filepath.Join(out, "config.json"))
	if err != nil {
		return "", err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", err
	}
	// Refuse a direct-only placeholder: it must not be presented as a usable
	// proxy configuration just because a source file parsed successfully.
	leaf := false
	for _, value := range root["outbounds"].([]any) {
		outbound := value.(map[string]any)
		kind, _ := outbound["type"].(string)
		if kind != "selector" && kind != "urltest" && kind != "direct" && kind != "block" {
			leaf = true
		}
	}
	if !leaf {
		return "", fmt.Errorf("add a node or subscription before generating")
	}
	// The host pipeline assumes populated buckets. A nodes-only mobile
	// profile still needs the named proxy/reject anchors, but they must match
	// nothing. Empty binary sets are valid; an empty route rule would match
	// everything, so remove those rules instead.
	route := root["route"].(map[string]any)
	sets, _ := route["rule_set"].([]any)
	known := map[string]bool{}
	for _, value := range sets {
		known[value.(map[string]any)["tag"].(string)] = true
	}
	for _, name := range []string{"proxy", "reject"} {
		if known["rs-"+name] {
			continue
		}
		p := filepath.Join(out, "rules", name+".srs")
		f, err := os.Create(p)
		if err != nil {
			return "", err
		}
		err = srs.Write(f, option.PlainRuleSet{Rules: []option.HeadlessRule{}}, C.RuleSetVersion3)
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		sets = append(sets, map[string]any{"type": "local", "tag": "rs-" + name, "format": "binary", "path": p})
	}
	route["rule_set"] = sets
	var rules []any
	for _, value := range route["rules"].([]any) {
		rule := value.(map[string]any)
		if rawNames, exists := rule["rule_set"]; exists {
			names, _ := rawNames.([]any)
			if len(names) == 0 {
				continue
			}
		}
		rules = append(rules, rule)
	}
	route["rules"] = rules
	raw, err = json.Marshal(root)
	if err != nil {
		return "", err
	}
	files := map[string][]byte{}
	entries, err := os.ReadDir(filepath.Join(out, "rules"))
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(out, "rules", entry.Name()))
		if err != nil {
			return "", err
		}
		files["rules/"+entry.Name()] = body
	}
	// Package the original source snapshot, not normalized compilation inputs.
	packageData := struct {
		Format           string            `json:"format"`
		Name             string            `json:"name"`
		Config           string            `json:"config"`
		Files            map[string][]byte `json:"files"`
		SourceBundleJSON string            `json:"sourceBundleJSON"`
		SourceDigest     string            `json:"sourceDigest"`
	}{"sakamoto-tunnel-v1", "Generated on device", string(raw), files, bundleJSON, ""}
	encoded, err := json.Marshal(packageData)
	return string(encoded), err
}

var generationURL = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"']+`)

func sanitizeGenerationError(err error) string {
	// Preserve stage/status/reason. URL paths, query tokens and credentials
	// never reach the UI, even when embedded in a nested network error.
	text := generationURL.ReplaceAllStringFunc(err.Error(), func(raw string) string {
		parsed, e := url.Parse(raw)
		if e != nil || parsed.Hostname() == "" {
			return "[remote source]"
		}
		return parsed.Hostname()
	})
	if len(text) > 1200 {
		text = text[:1200]
	}
	return text
}
