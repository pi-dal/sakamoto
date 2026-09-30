package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is sakamoto's sidecar configuration, separate from sing-box config.json.
type Config struct {
	API struct {
		URL    string `yaml:"url"`    // sing-box api service, e.g. http://127.0.0.1:9090
		Secret string `yaml:"secret"` // api service secret
	} `yaml:"api"`

	// Fallbacks maps selector tags to ordered chains of their outbound members.
	// Prefer the first healthy member; fail over when selected member dies and
	// switch back after recover_after consecutive successful health checks.
	Fallbacks       map[string][]string `yaml:"fallbacks"`
	FallbackEnabled bool                `yaml:"fallback_enabled"` // automatic RealityAuto → OthersAuto failover

	CheckInterval time.Duration `yaml:"check_interval"` // default 30s
	RecoverAfter  int           `yaml:"recover_after"`  // default 2
	TestSettle    time.Duration `yaml:"test_settle"`    // wait for URLTest results; default 5s

	// Import sources.
	Subscriptions []SubSource `yaml:"subscriptions"` // optional HTTP(S) subscription sources
	NodesFile     string      `yaml:"nodes_file"`    // manual share links
	ConfPath      string      `yaml:"conf"`          // Shadowrocket .conf rule skeleton
	SRJSONPath    string      `yaml:"srjson"`        // Shadowrocket JSON export for manual nodes
	AllowHosts    []string    `yaml:"allow_hosts"`   // host allowlist for manual nodes from srjson
	SocksExit     string      `yaml:"socks_exit"`    // chained SOCKS exit tag (detours to MainProxy)
	BlockQUIC     bool        `yaml:"block_quic"`    // reject UDP:443 to force TCP; default true
	BlockSTUN     bool        `yaml:"block_stun"`    // reject detected STUN; default true
	Experiment    struct {
		Mode      string `yaml:"mode"`      // off: direct fallback; on: proxy fallback; auto: learn failed hosts
		Threshold int    `yaml:"threshold"` // distinct spaced direct failures; default 3
	} `yaml:"experiment"`

	// Shadowrocket application-level setting equivalents.
	ChainEnabled bool   `yaml:"chain_enabled"` // ChainProxyEnabled: SOCKS detours to MainProxy
	StrictRoute  bool   `yaml:"strict_route"`  // TunnelEnforceRoutesKey; default false
	TunStack     string `yaml:"tun_stack"`     // system/gvisor/mixed; default gvisor
	LogLevel     string `yaml:"log_level"`     // info/debug; default info
	MixedInbound struct {
		Enabled  bool `yaml:"enabled"`   // local HTTP/SOCKS mixed inbound
		Port     int  `yaml:"port"`      // default 2334
		AllowLAN bool `yaml:"allow_lan"` // allow LAN clients
	} `yaml:"mixed_inbound"`
	SystemProxy struct {
		Enabled bool   `yaml:"enabled"` // browser hostname proxy alongside the TUN; restored on disconnect
		Service string `yaml:"service"` // macOS network service; default Wi-Fi
	} `yaml:"system_proxy"`
	DNSGuard struct {
		Enabled             bool     `yaml:"enabled"`       // native DNS + system takeover; opt-in
		Service             string   `yaml:"service"`       // macOS network service
		LocalDomains        []string `yaml:"local_domains"` // explicitly private namespaces only
		BootstrapIP         string   `yaml:"bootstrap_ip"`  // direct, certificate-verified IP-pinned DoH
		BootstrapServerName string   `yaml:"bootstrap_server_name"`
	} `yaml:"dns_guard"`
	ICloud struct {
		Enabled     bool     `yaml:"enabled"`      // optional, off by default; uploads configured source files
		IncludeConf bool     `yaml:"include_conf"` // include the local conf and its relative .conf dependencies
		Directory   string   `yaml:"directory"`    // absolute iCloud Drive directory
		Files       []string `yaml:"files"`        // additional relative source paths; generated files are forbidden
	} `yaml:"icloud"`
	TailscaleOptimize bool   `yaml:"tailscale_optimize"` // auto-detect Tailscale; default true
	UTLSFingerprint   string `yaml:"utls_fingerprint"`   // global fingerprint override; empty uses node links

	// URLTest parameters match Shadowrocket's interval/tolerance/timeout/URL.
	URLTest struct {
		URL       string `yaml:"url"`       // default gstatic 204
		Interval  string `yaml:"interval"`  // default 10m
		Tolerance int    `yaml:"tolerance"` // default 100ms
	} `yaml:"urltest"`
}

// SubSource identifies a subscription URL and its format (auto detects it).
type SubSource struct {
	Name   string `yaml:"name"`
	URL    string `yaml:"url"`
	Format string `yaml:"format"` // auto | singbox | clash | base64
}

func DefaultDir() string {
	if dir := os.Getenv("SAKAMOTO_DIR"); dir != "" {
		return dir
	}
	// Existing installations keep using the old directory until their root
	// LaunchDaemon is migrated. Do not silently detach the TUI from a live VPN.
	old := legacyDir()
	if c, err := net.DialTimeout("unix", filepath.Join(old, "svc.sock"), 150*time.Millisecond); err == nil {
		_ = c.Close() // probe only: no data to flush
		return old
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sakamoto")
}
func legacyDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sakamoto")
}

func Default() *Config {
	c := &Config{
		Fallbacks:       map[string][]string{"MainProxy": {"RealityAuto", "OthersAuto"}},
		FallbackEnabled: true,
		CheckInterval:   30 * time.Second,
		RecoverAfter:    2,
		TestSettle:      5 * time.Second,
	}
	c.API.URL = "http://127.0.0.1:9090"
	c.API.Secret, _ = NewAPISecret() // generation failure is rejected by ValidateAPISecret before use
	home, _ := os.UserHomeDir()
	c.ConfPath = filepath.Join(home, "Downloads", "sr_top500_banlist_ad.conf")
	c.SRJSONPath = filepath.Join(home, "Documents", "Shadowrocket.json")
	c.NodesFile = filepath.Join(DefaultDir(), "nodes.txt")
	c.AllowHosts = []string{} // Empty by default: use nodes.txt rather than stale export entries.
	c.BlockQUIC = true
	c.BlockSTUN = true // Mirrors Shadowrocket UDPSocketDisableSTUN=true.
	c.Experiment.Mode = "off"
	c.Experiment.Threshold = 3
	c.ChainEnabled = true
	c.StrictRoute = false // TunnelEnforceRoutesKey=false
	c.TunStack = "gvisor"
	c.LogLevel = "info"
	c.MixedInbound.Enabled = false
	c.MixedInbound.Port = 2334
	c.MixedInbound.AllowLAN = false
	c.SystemProxy.Service = "Wi-Fi"
	c.DNSGuard.Service = "Wi-Fi"
	c.DNSGuard.LocalDomains = []string{"local", "lan"}
	c.DNSGuard.BootstrapIP = "1.12.12.12"
	c.DNSGuard.BootstrapServerName = "doh.pub"
	c.ICloud.Directory = filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "sakamoto")
	c.ICloud.Files = []string{"nodes.txt"}
	c.ICloud.IncludeConf = true
	c.TailscaleOptimize = true
	c.URLTest.URL = "https://www.gstatic.com/generate_204"
	c.URLTest.Interval = "10m"
	c.URLTest.Tolerance = 100
	return c
}

func Load(path string) (*Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) && path == DefaultPath() {
		legacy := filepath.Join(legacyDir(), "sakamoto.yaml")
		if old, oldErr := os.ReadFile(legacy); oldErr == nil {
			b = old
			err = nil
		}
	}
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil // No sidecar yet; use defaults.
		}
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, err
	}
	// Old sidecars have no experiment setting: retain a previously generated
	// proxy final rather than silently changing them to the new off=direct mode.
	var marker struct {
		Experiment *yaml.Node `yaml:"experiment"`
		ICloud     *struct {
			IncludeConf *bool `yaml:"include_conf"`
		} `yaml:"icloud"`
	}
	if err := yaml.Unmarshal(b, &marker); err != nil {
		return nil, err
	}
	// An old nodes-only consent must not silently expand to private confs.
	// Fresh configs use the new default; existing iCloud sections opt in by
	// explicitly setting include_conf or confirming the new TUI control.
	if marker.ICloud != nil && marker.ICloud.IncludeConf == nil {
		c.ICloud.IncludeConf = false
	}
	if marker.Experiment == nil {
		raw, e := os.ReadFile(filepath.Join(filepath.Dir(path), "config.json"))
		if e == nil {
			var prior struct {
				Route struct {
					Final string `json:"final"`
				} `json:"route"`
			}
			if json.Unmarshal(raw, &prior) == nil && prior.Route.Final != "" && prior.Route.Final != "direct" {
				c.Experiment.Mode = "on"
			}
		}
	}
	if err := c.ValidateExperiment(); err != nil {
		return nil, err
	}
	if err := c.ValidateAPIEndpoint(); err != nil {
		return nil, err
	}
	if err := c.ValidateDNSGuard(); err != nil {
		return nil, err
	}
	return c, nil
}

// NewAPISecret creates 32 bytes of cryptographically random API key material.
func NewAPISecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ValidateAPISecret rejects placeholders and short keys rather than exposing
// the unauthenticated loopback API when a template is left unchanged.
func ValidateAPISecret(secret string) error {
	s := strings.TrimSpace(secret)
	if len(s) < 32 || strings.EqualFold(s, "replace_with_random_secret") || strings.EqualFold(s, "change-me") {
		return errors.New("API secret must be a unique random value of at least 32 characters; run sakamoto rotate-api")
	}
	return nil
}

// DefaultPath returns the sidecar path under the active runtime directory.
func DefaultPath() string { return filepath.Join(DefaultDir(), "sakamoto.yaml") }

// ValidateAPIEndpoint prevents the credential from being sent to a remote host.
func (c *Config) ValidateAPIEndpoint() error {
	u, err := url.Parse(c.API.URL)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("api.url must be a loopback HTTP endpoint with explicit port")
	}
	return nil
}

// ValidateDNSGuard validates system-service and TLS-bootstrap inputs.
func (c *Config) ValidateDNSGuard() error {
	if !c.DNSGuard.Enabled {
		return nil
	}
	if strings.TrimSpace(c.DNSGuard.Service) == "" || strings.HasPrefix(c.DNSGuard.Service, "-") || len(c.DNSGuard.Service) >= 256 || strings.ContainsAny(c.DNSGuard.Service, "\x00\r\n") {
		return errors.New("dns_guard.service must name a macOS network service")
	}
	ip := net.ParseIP(c.DNSGuard.BootstrapIP)
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() {
		return errors.New("dns_guard.bootstrap_ip must be a public literal IP")
	}
	u, err := url.Parse("https://" + c.DNSGuard.BootstrapServerName)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Hostname() != c.DNSGuard.BootstrapServerName || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("dns_guard.bootstrap_server_name must be a TLS hostname")
	}
	for _, domain := range c.DNSGuard.LocalDomains {
		if domain == "" || strings.ContainsAny(domain, " /\\\x00\r\n*") {
			return errors.New("dns_guard.local_domains must contain explicit private domain suffixes")
		}
	}
	return nil
}

// ValidateExperiment rejects unknown modes instead of silently widening direct access.
func (c *Config) ValidateExperiment() error {
	switch c.Experiment.Mode {
	case "off", "on", "auto":
	default:
		return errors.New("experiment.mode must be off, on, or auto")
	}
	if c.Experiment.Threshold < 1 || c.Experiment.Threshold > 20 {
		return errors.New("experiment.threshold must be 1–20")
	}
	return nil
}

// MarshalYAML serializes the sidecar without writing secrets to stdout.
func (c *Config) MarshalYAML() ([]byte, error) { return yaml.Marshal(c) }

// Save writes settings without allowing a TUI opened before a credential
// rotation to silently put the old API key back on disk.
func (c *Config) Save(path string) error {
	if err := c.ValidateDNSGuard(); err != nil {
		return err
	}
	if raw, err := os.ReadFile(path); err == nil {
		var current struct {
			API struct {
				URL    string `yaml:"url"`
				Secret string `yaml:"secret"`
			} `yaml:"api"`
		}
		if err := yaml.Unmarshal(raw, &current); err != nil {
			return err
		}
		if current.API.Secret != "" {
			c.API.Secret = current.API.Secret
		}
		if current.API.URL != "" {
			c.API.URL = current.API.URL
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	b, err := c.MarshalYAML()
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
