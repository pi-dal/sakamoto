package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/icloud"
)

type ConfigurationField struct {
	Key      string
	Section  string
	Label    string
	Kind     string
	Value    string
	Choices  []string
	ReadOnly bool
}

type Configuration struct {
	Fields            []ConfigurationField
	SubscriptionCount int
	PolicyCount       int
	NodesPath         string
}

const (
	ConfigBool   = "bool"
	ConfigChoice = "choice"
	ConfigText   = "text"
)

func configurationFromConfig(c *config.Config) Configuration {
	field := func(section, key, label, kind, value string, choices ...string) ConfigurationField {
		return ConfigurationField{Section: section, Key: key, Label: label, Kind: kind, Value: value, Choices: choices}
	}
	boolValue := func(v bool) string {
		if v {
			return "on"
		}
		return "off"
	}
	return Configuration{
		Fields: []ConfigurationField{
			field("Network", "chain_enabled", "Chain SOCKS exit", ConfigBool, boolValue(c.ChainEnabled)),
			field("Network", "strict_route", "Strict routing", ConfigBool, boolValue(c.StrictRoute)),
			field("Network", "tun_stack", "TUN stack", ConfigChoice, c.TunStack, "system", "gvisor", "mixed"),
			field("DNS", "dns_enabled", "Protected system DNS", ConfigBool, boolValue(c.DNSGuard.Enabled)),
			field("DNS", "dns_service", "DNS network service", ConfigText, c.DNSGuard.Service),
			field("DNS", "dns_local_domains", "Private DNS suffixes", ConfigText, strings.Join(c.DNSGuard.LocalDomains, ", ")),
			field("Traffic policy", "cf_region_block", "CF region auto-proxy", ConfigBool, boolValue(c.Experiment.CFRegionBlock)),
			field("Traffic policy", "experiment_mode", "Unmatched policy", ConfigChoice, c.Experiment.Mode, "off", "on", "auto"),
			field("Traffic policy", "experiment_threshold", "Direct failure threshold", ConfigText, strconv.Itoa(c.Experiment.Threshold)),
			field("Privacy", "block_stun", "Block STUN / WebRTC", ConfigBool, boolValue(c.BlockSTUN)),
			field("Privacy", "block_quic", "Block QUIC (UDP 443)", ConfigBool, boolValue(c.BlockQUIC)),
			field("Local proxy", "mixed_enabled", "Enable HTTP/SOCKS", ConfigBool, boolValue(c.MixedInbound.Enabled)),
			field("Local proxy", "system_proxy_enabled", "System proxy (browser)", ConfigBool, boolValue(c.SystemProxy.Enabled)),
			field("Local proxy", "system_proxy_service", "Network service", ConfigText, c.SystemProxy.Service),
			field("Local proxy", "mixed_allow_lan", "Allow LAN clients", ConfigBool, boolValue(c.MixedInbound.AllowLAN)),
			field("Local proxy", "mixed_port", "Listen port", ConfigText, strconv.Itoa(c.MixedInbound.Port)),
			field("Advanced", "log_level", "Log level", ConfigChoice, c.LogLevel, "error", "warn", "info", "debug"),
			field("Advanced", "urltest_url", "Latency test URL", ConfigText, c.URLTest.URL),
			field("Advanced", "urltest_interval", "Test interval", ConfigText, c.URLTest.Interval),
			field("Advanced", "urltest_tolerance", "Latency tolerance", ConfigText, strconv.Itoa(c.URLTest.Tolerance)),
			field("Advanced", "utls_fingerprint", "uTLS fingerprint", ConfigText, c.UTLSFingerprint),
			field("Advanced", "check_interval", "Health check interval", ConfigText, c.CheckInterval.String()),
			field("Advanced", "recover_after", "Recovery threshold", ConfigText, strconv.Itoa(c.RecoverAfter)),
			field("Cloud sync", "icloud_enabled", "Sync sources to iCloud", ConfigBool, boolValue(c.ICloud.Enabled)),
			field("Cloud sync", "icloud_include_conf", "Include conf and rule includes", ConfigBool, boolValue(c.ICloud.IncludeConf)),
			field("Cloud sync", "icloud_directory", "iCloud directory", ConfigText, c.ICloud.Directory),
			field("Cloud sync", "icloud_files", "Additional source paths", ConfigText, strings.Join(c.ICloud.Files, ", ")),
			field("Cloud sync", "s3_enabled", "Sync sources to S3", ConfigBool, boolValue(c.S3.Enabled)),
			field("Cloud sync", "s3_endpoint", "S3 endpoint", ConfigText, c.S3.Endpoint),
			field("Cloud sync", "s3_region", "S3 region", ConfigText, c.S3.Region),
			field("Cloud sync", "s3_bucket", "S3 bucket", ConfigText, c.S3.Bucket),
			field("Cloud sync", "s3_prefix", "S3 prefix", ConfigText, c.S3.Prefix),
			field("Fallback", "fallback_enabled", "Automatic fallback", ConfigBool, boolValue(c.FallbackEnabled)),
			field("Fallback", "fallback_chain", "Fallback chain", ConfigText, strings.Join(c.Fallbacks["MainProxy"], ", ")),
		},
		SubscriptionCount: len(c.Subscriptions),
		PolicyCount:       len(c.PolicyRules),
		NodesPath:         c.NodesFile,
	}
}

func (c *Controller) Configuration() (Configuration, error) {
	cfg, err := config.Load(c.path)
	if err != nil {
		return Configuration{}, err
	}
	return configurationFromConfig(cfg), nil
}

func (c *Controller) SaveConfiguration(ctx context.Context, value Configuration) error {
	err := c.perform(ctx, "Saving configuration", func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		cfg, err := config.Load(c.path)
		if err != nil {
			return err
		}
		if err := applyConfigurationFields(cfg, value.Fields); err != nil {
			return err
		}
		return saveConfigurationAtomically(cfg, c.path)
	})
	c.update(func(s *Snapshot) {
		if err == nil {
			s.Notice = core.Successf("Configuration saved. Regenerate and reconnect to apply.")
		}
	})
	return err
}

func saveConfigurationAtomically(cfg *config.Config, path string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sakamoto-config-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := cfg.Save(tmpPath); err != nil {
		return err
	}
	if _, err := config.Load(tmpPath); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func applyConfigurationFields(cfg *config.Config, fields []ConfigurationField) error {
	for _, field := range fields {
		value := strings.TrimSpace(field.Value)
		if err := applyConfigurationField(cfg, field.Key, value); err != nil {
			return fmt.Errorf("%s: %w", field.Label, err)
		}
	}
	if err := config.ValidateAPISecret(cfg.API.Secret); err != nil {
		return err
	}
	if err := cfg.ValidateAPIEndpoint(); err != nil {
		return err
	}
	if err := cfg.ValidateExperiment(); err != nil {
		return err
	}
	if err := cfg.ValidateDNSGuard(); err != nil {
		return err
	}
	if err := cfg.ValidatePolicy(); err != nil {
		return err
	}
	return cfg.ValidateAutomaticGroups()
}

func applyConfigurationField(cfg *config.Config, key, value string) error {
	boolValue := func() (bool, error) {
		switch strings.ToLower(value) {
		case "on", "true", "1":
			return true, nil
		case "off", "false", "0":
			return false, nil
		default:
			return false, errors.New("choose on or off")
		}
	}
	intValue := func(minimum, maximum int) (int, error) {
		v, err := strconv.Atoi(value)
		if err != nil || v < minimum || v > maximum {
			return 0, fmt.Errorf("must be between %d and %d", minimum, maximum)
		}
		return v, nil
	}
	durationValue := func() (time.Duration, error) {
		v, err := time.ParseDuration(value)
		if err != nil || v < time.Second {
			return 0, errors.New("must be at least 1 second")
		}
		return v, nil
	}
	switch key {
	case "chain_enabled":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.ChainEnabled = v
	case "strict_route":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.StrictRoute = v
	case "tun_stack":
		if value != "system" && value != "gvisor" && value != "mixed" {
			return errors.New("choose system, gvisor, or mixed")
		}
		cfg.TunStack = value
	case "dns_enabled":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.DNSGuard.Enabled = v
	case "dns_service":
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("enter a network service")
		}
		cfg.DNSGuard.Service = value
	case "dns_local_domains":
		parts := splitConfigurationList(value)
		for _, part := range parts {
			if part == "" || strings.ContainsAny(part, " /\\\x00\r\n*") {
				return errors.New("use explicit private domain suffixes")
			}
		}
		cfg.DNSGuard.LocalDomains = parts
	case "cf_region_block":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.Experiment.CFRegionBlock = v
	case "experiment_mode":
		if value != "off" && value != "on" && value != "auto" {
			return errors.New("choose off, on, or auto")
		}
		cfg.Experiment.Mode = value
	case "experiment_threshold":
		v, err := intValue(1, 20)
		if err != nil {
			return err
		}
		cfg.Experiment.Threshold = v
	case "block_stun":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.BlockSTUN = v
	case "block_quic":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.BlockQUIC = v
	case "mixed_enabled":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.MixedInbound.Enabled = v
	case "system_proxy_enabled":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.SystemProxy.Enabled = v
	case "system_proxy_service":
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("enter a network service")
		}
		cfg.SystemProxy.Service = value
	case "mixed_allow_lan":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.MixedInbound.AllowLAN = v
	case "mixed_port":
		v, err := intValue(1, 65535)
		if err != nil {
			return err
		}
		cfg.MixedInbound.Port = v
	case "log_level":
		if value != "error" && value != "warn" && value != "info" && value != "debug" {
			return errors.New("choose error, warn, info, or debug")
		}
		cfg.LogLevel = value
	case "urltest_url":
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("enter a valid HTTP(S) URL")
		}
		cfg.URLTest.URL = value
	case "urltest_interval":
		v, err := time.ParseDuration(value)
		if err != nil || v < time.Second {
			return errors.New("enter an interval of at least 1 second")
		}
		cfg.URLTest.Interval = value
	case "urltest_tolerance":
		v, err := intValue(0, 10000)
		if err != nil {
			return err
		}
		cfg.URLTest.Tolerance = v
	case "utls_fingerprint":
		cfg.UTLSFingerprint = value
	case "check_interval":
		v, err := durationValue()
		if err != nil {
			return err
		}
		cfg.CheckInterval = v
	case "recover_after":
		v, err := intValue(1, 20)
		if err != nil {
			return err
		}
		cfg.RecoverAfter = v
	case "icloud_enabled":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.ICloud.Enabled = v
	case "icloud_include_conf":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.ICloud.IncludeConf = v
	case "icloud_directory":
		if !filepath.IsAbs(value) {
			return errors.New("path must be absolute")
		}
		cfg.ICloud.Directory = value
	case "icloud_files":
		parts := splitConfigurationList(value)
		for _, part := range parts {
			if !icloud.ValidSourceName(part) {
				return errors.New("source paths must be relative and point to source files")
			}
		}
		cfg.ICloud.Files = parts
	case "s3_enabled":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.S3.Enabled = v
	case "s3_endpoint":
		cfg.S3.Endpoint = value
	case "s3_region":
		cfg.S3.Region = value
	case "s3_bucket":
		cfg.S3.Bucket = value
	case "s3_prefix":
		cfg.S3.Prefix = value
	case "fallback_enabled":
		v, err := boolValue()
		if err != nil {
			return err
		}
		cfg.FallbackEnabled = v
	case "fallback_chain":
		parts := splitConfigurationList(value)
		if len(parts) == 0 {
			return errors.New("enter at least one fallback group")
		}
		cfg.Fallbacks["MainProxy"] = parts
	default:
		return fmt.Errorf("unsupported setting %q", key)
	}
	return nil
}

func splitConfigurationList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}
