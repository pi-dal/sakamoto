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

// Config 是 sakamoto 的侧车配置（与 sing-box config.json 分离，由 sbt 独占解释）。
type Config struct {
	API struct {
		URL    string `yaml:"url"`    // sing-box api service, e.g. http://127.0.0.1:9090
		Secret string `yaml:"secret"` // api service secret
	} `yaml:"api"`

	// Fallbacks: selector tag → 有序降级链（成员必须是该 selector 的 outbounds）。
	// 语义：链首优先；当前选中死亡 → 选第一个存活的；更优候选恢复 recover_after 次后回切。
	Fallbacks       map[string][]string `yaml:"fallbacks"`
	FallbackEnabled bool                `yaml:"fallback_enabled"` // automatic RealityAuto → OthersAuto failover

	CheckInterval time.Duration `yaml:"check_interval"` // 默认 30s
	RecoverAfter  int           `yaml:"recover_after"`  // 默认 2
	TestSettle    time.Duration `yaml:"test_settle"`    // URLTest 后等待结果的时间，默认 5s

	// import 源定义
	Subscriptions []SubSource `yaml:"subscriptions"` // 可选 HTTP(S) 订阅源
	NodesFile     string      `yaml:"nodes_file"`    // 手动分享链接文件
	ConfPath      string      `yaml:"conf"`          // Shadowrocket .conf（规则骨架）
	SRJSONPath    string      `yaml:"srjson"`        // Shadowrocket.json 备份（手动节点提取）
	AllowHosts    []string    `yaml:"allow_hosts"`   // srjson 中手动节点 host 白名单
	SocksExit     string      `yaml:"socks_exit"`    // 链式出口节点 tag（该 socks 节点自动 detour 到 MainProxy）
	BlockQUIC     bool        `yaml:"block_quic"`    // 拒 UDP:443 强制 TCP（默认 true）
	BlockSTUN     bool        `yaml:"block_stun"`    // 拒 STUN 防 WebRTC 泄露（默认 true，对齐 UDPSocketDisableSTUN）
	Experiment    struct {
		Mode      string `yaml:"mode"`      // off: 保持来源 FINAL; on: 未命中走代理; auto: 失败达阈值的域名走代理
		Threshold int    `yaml:"threshold"` // auto: 不同尝试连续失败次数（默认 3）
	} `yaml:"experiment"`

	// —— 对齐 Shadowrocket 应用设置层 ——
	ChainEnabled bool   `yaml:"chain_enabled"` // ChainProxyEnabled：socks 出口 detour MainProxy（默认 true）
	StrictRoute  bool   `yaml:"strict_route"`  // TunnelEnforceRoutesKey：TUN 抢占路由（默认 false）
	TunStack     string `yaml:"tun_stack"`     // TUN 栈：system/gvisor/mixed（默认 system）
	LogLevel     string `yaml:"log_level"`     // DebugLoggingEnabled: info/debug（默认 info）
	MixedInbound struct {
		Enabled  bool `yaml:"enabled"`   // ProxyServerType：本地 HTTP/SOCKS 混合代理口
		Port     int  `yaml:"port"`      // 默认 2334（对齐 SR ProxyServerPort）
		AllowLAN bool `yaml:"allow_lan"` // ProxyShareEnabled：允许局域网设备连入
	} `yaml:"mixed_inbound"`
	SystemProxy struct {
		Enabled bool   `yaml:"enabled"` // 与 TUN 同时为浏览器提供域名代理，断开时恢复原设置
		Service string `yaml:"service"` // macOS 网络服务名，默认 Wi-Fi
	} `yaml:"system_proxy"`
	ICloud struct {
		Enabled   bool     `yaml:"enabled"`   // optional, off by default; uploads node links to iCloud Drive
		Directory string   `yaml:"directory"` // absolute iCloud Drive directory
		Files     []string `yaml:"files"`     // source filenames only; generated files are forbidden
	} `yaml:"icloud"`
	TailscaleOptimize bool   `yaml:"tailscale_optimize"` // Tailscale 自动检测优化（默认 true）
	UTLSFingerprint   string `yaml:"utls_fingerprint"`   // SR Fingerprint 全局指纹（空=用链接自带 fp；设置则覆盖所有节点，对齐 SR 全局生效）

	// URLTest 组参数（对齐 SR url-test interval/tolerance/timeout/url）
	URLTest struct {
		URL       string `yaml:"url"`       // 默认 gstatic 204
		Interval  string `yaml:"interval"`  // 默认 10m（SR 600s）
		Tolerance int    `yaml:"tolerance"` // 默认 100（SR 100ms）
	} `yaml:"urltest"`
}

// SubSource 订阅源：URL + 格式（auto 自动嗅探）。
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
	c.AllowHosts = []string{} // 默认空：手动节点全走 nodes.txt，避免 SR 备份里的过期节点
	c.BlockQUIC = true
	c.BlockSTUN = true // 对齐 SR UDPSocketDisableSTUN=true
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
	c.ICloud.Directory = filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "sakamoto")
	c.ICloud.Files = []string{"nodes.txt"}
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
			return c, nil // 无配置文件时用默认值
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
	}
	if err := yaml.Unmarshal(b, &marker); err != nil {
		return nil, err
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

// DefaultPath 返回默认配置路径 ~/.config/sakamoto/sakamoto.yaml
func DefaultPath() string { return filepath.Join(DefaultDir(), "sakamoto.yaml") }

// ValidateAPIEndpoint prevents the credential from being sent to a remote host.
func (c *Config) ValidateAPIEndpoint() error {
	u, err := url.Parse(c.API.URL)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("api.url must be a loopback HTTP endpoint with explicit port")
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
