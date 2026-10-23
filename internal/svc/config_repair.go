package svc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/restorepoint"
	"gopkg.in/yaml.v3"
)

const configRepairCapability = "configuration-repair-v1"

type repairFailure struct {
	problem string
	err     error
}

func (e *repairFailure) Error() string            { return e.err.Error() }
func (e *repairFailure) Unwrap() error            { return e.err }
func repairIssue(problem string, err error) error { return &repairFailure{problem, err} }
func problemFor(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var failure *repairFailure
	if errors.As(err, &failure) {
		return failure.problem
	}
	return "core_start_failed"
}

type configurationInput struct {
	raw    []byte
	exists bool
}
type preparedConfiguration struct {
	point      *restorepoint.Point
	temporary  string
	original   map[string]configurationInput
	generation string
	keep       bool
}

func (p *preparedConfiguration) Close() {
	if p.temporary != "" && !p.keep {
		_ = os.RemoveAll(p.temporary)
	}
}
func (p *preparedConfiguration) unchanged(dir string) error {
	for name, want := range p.original {
		got, err := configurationRead(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if got.exists != want.exists || !bytes.Equal(got.raw, want.raw) {
			return repairIssue("inputs_changed", fmt.Errorf("%s changed during configuration preparation", name))
		}
	}
	token, err := recovery.RulesGeneration(dir)
	if err != nil {
		return err
	}
	if token != p.generation {
		return repairIssue("inputs_changed", errors.New("rules generation changed during preparation"))
	}
	return nil
}
func (p *preparedConfiguration) Apply(ctx context.Context, dir string) error {
	return p.point.ApplyChecked(dir, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if recovery.Held(dir) {
			return repairIssue("maintenance", errors.New("maintenance began during preparation"))
		}
		if err := p.unchanged(dir); err != nil {
			return err
		}
		if _, err := restorepoint.BackupCandidate(dir); err != nil {
			return err
		}
		// A partial write can already reference this generation. Retain its assets
		// even when a later fsync/write fails, and leave the circuit blocked.
		p.keep = true
		return nil
	})
}
func configurationRead(path string) (configurationInput, error) {
	raw, err := recovery.ReadPrivate(path, 64<<20)
	if errors.Is(err, os.ErrNotExist) {
		return configurationInput{}, nil
	}
	if err != nil {
		return configurationInput{}, err
	}
	return configurationInput{raw: raw, exists: true}, nil
}
func repairDirectory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("unsafe configuration recovery directory")
	}
	if os.Geteuid() == 0 {
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
			return errors.New("configuration recovery directory is not root-owned")
		}
	}
	return nil
}
func (s *Server) prepareConfiguration(ctx context.Context, options RepairOptions) (p *preparedConfiguration, err error) {
	p = &preparedConfiguration{original: map[string]configurationInput{}}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()
	// Snapshot under the same short transaction used by learning and activation.
	unlock, err := recovery.LockRules(s.workDir)
	if err != nil {
		return p, err
	}
	for _, name := range []string{"config.json", "sakamoto.yaml", "auto-proxy.json", "auto-proxy-rules.json", "api-rotation.pending.json"} {
		value, e := configurationRead(filepath.Join(s.workDir, name))
		if e != nil {
			unlock()
			return p, repairIssue("config_invalid", e)
		}
		p.original[name] = value
	}
	p.generation, err = recovery.RulesGeneration(s.workDir)
	unlock()
	if err != nil {
		return p, err
	}
	if p.original["api-rotation.pending.json"].exists {
		return p, repairIssue("pending_api_rotation", errors.New("apply or cancel the staged API rotation before replacing configuration"))
	}
	if options.Fallback {
		p.point, err = restorepoint.Load(s.workDir)
		if err != nil {
			return p, repairIssue("fallback_unavailable", err)
		}
		if err = checkConfigurationPair(filepath.Join(p.point.Dir, "sakamoto.yaml"), filepath.Join(p.point.Dir, "config.json")); err != nil {
			return p, repairIssue("fallback_unavailable", err)
		}
		// A retained live-rule snapshot must be checked against its saved bytes,
		// even if the current live file is missing or corrupt.
		raw, e := os.ReadFile(filepath.Join(p.point.Dir, "config.json"))
		if e != nil {
			return p, e
		}
		raw, e = rebaseConfiguration(raw, p.point.Dir, s.workDir, p.point.Dir)
		if e != nil {
			return p, e
		}
		check, e := os.CreateTemp(p.point.Dir, ".fallback-check-*.json")
		if e != nil {
			return p, e
		}
		checkPath := check.Name()
		defer func() { _ = os.Remove(checkPath) }()
		if _, e = check.Write(raw); e != nil {
			_ = check.Close()
			return p, e
		}
		if e = check.Close(); e != nil {
			return p, e
		}
		if err = checkNativeConfiguration(ctx, filepath.Join(p.point.Dir, "engine"), s.workDir, checkPath); err != nil {
			return p, repairIssue("fallback_unavailable", err)
		}
		return p, nil
	}
	if !p.original["sakamoto.yaml"].exists {
		return p, repairIssue("generation_failed", errors.New("source sidecar is missing; restore a known-working configuration or the source settings"))
	}
	// Load validates user settings. Require the API identity to actually exist
	// in the file: Config.Load's fresh-install defaults are inappropriate here.
	var identity struct {
		API        struct{ URL, Secret string } `yaml:"api"`
		Experiment *struct {
			Mode string `yaml:"mode"`
		} `yaml:"experiment"`
		DNSGuard *struct {
			Enabled bool `yaml:"enabled"`
		} `yaml:"dns_guard"`
	}
	if err = yaml.Unmarshal(p.original["sakamoto.yaml"].raw, &identity); err != nil {
		return p, repairIssue("generation_failed", err)
	}
	if identity.API.URL == "" || config.ValidateAPISecret(identity.API.Secret) != nil {
		return p, repairIssue("generation_failed", errors.New("source sidecar has no valid saved API identity"))
	}
	if identity.Experiment == nil && !json.Valid(p.original["config.json"].raw) {
		return p, repairIssue("generation_failed", errors.New("routing mode is missing and the previous core configuration cannot establish it"))
	}
	if identity.DNSGuard == nil {
		s.mu.Lock()
		protected := s.dnsStatus == "protected"
		s.mu.Unlock()
		_, snapshotErr := os.Lstat(s.dnsStatePath())
		if protected || snapshotErr == nil {
			return p, repairIssue("generation_failed", errors.New("source sidecar does not specify the existing DNS protection policy"))
		}
	}
	base := filepath.Join(s.workDir, "recovery")
	if err = repairDirectory(base); err != nil {
		return p, err
	}
	base = filepath.Join(base, "generated")
	if err = repairDirectory(base); err != nil {
		return p, err
	}
	p.temporary, err = os.MkdirTemp(base, "generation-")
	if err != nil {
		return p, err
	}
	manifest := restorepoint.Manifest{Files: map[string]string{}}
	for _, name := range []string{"config.json", "sakamoto.yaml", "auto-proxy.json", "auto-proxy-rules.json"} {
		if value := p.original[name]; value.exists {
			if err = recovery.Atomic(filepath.Join(p.temporary, name), value.raw, 0600); err != nil {
				return p, err
			}
			manifest.Files[name] = "prepared"
		}
	}
	// Preserve already downloaded GeoIP inputs; recovery should not download
	// unchanged rule sets when the local cache is available.
	cached, _ := filepath.Glob(filepath.Join(s.workDir, "rules", "geoip-*.srs"))
	if len(cached) > 0 {
		if err = repairDirectory(filepath.Join(p.temporary, "rules")); err != nil {
			return p, err
		}
	}
	for _, path := range cached {
		raw, e := os.ReadFile(path)
		if e != nil {
			return p, e
		}
		if err = recovery.Atomic(filepath.Join(p.temporary, "rules", filepath.Base(path)), raw, 0600); err != nil {
			return p, err
		}
	}
	cfg, e := config.Load(filepath.Join(p.temporary, "sakamoto.yaml"))
	if e != nil {
		return p, repairIssue("generation_failed", e)
	}
	if err = gen.Run(gen.Options{Context: ctx, RequireNodes: true, ConfPath: cfg.ConfPath, SRJSONPath: cfg.SRJSONPath, NodesFile: cfg.NodesFile, AllowHosts: strings.Join(cfg.AllowHosts, ","), Cfg: cfg, OutDir: p.temporary, Quiet: true, HTTPClient: &http.Client{Timeout: 15 * time.Second}}); err != nil {
		return p, repairIssue("generation_failed", err)
	}
	candidate := filepath.Join(p.temporary, "config.json")
	raw, e := os.ReadFile(candidate)
	if e != nil {
		return p, e
	}
	raw, e = rebaseConfiguration(raw, p.temporary, s.workDir, p.temporary)
	if e != nil {
		return p, e
	}
	if err = recovery.Atomic(candidate, raw, 0600); err != nil {
		return p, err
	}
	if err = checkConfigurationPair(filepath.Join(p.temporary, "sakamoto.yaml"), candidate); err != nil {
		return p, repairIssue("config_invalid", err)
	}
	if err = checkNativeConfiguration(ctx, singBoxExecutable(), s.workDir, candidate); err != nil {
		return p, repairIssue("config_invalid", err)
	}
	// Only the live learned rule remains mutable at the runtime location. All
	// compiled inputs stay in the retained, checked generation directory.
	raw, e = rebaseConfiguration(raw, p.temporary, s.workDir, s.workDir)
	if e != nil {
		return p, e
	}
	if err = recovery.Atomic(candidate, raw, 0600); err != nil {
		return p, err
	}
	manifest.Files["config.json"] = "prepared"
	if _, e = os.Stat(filepath.Join(p.temporary, "auto-proxy-rules.json")); e == nil {
		manifest.Files["auto-proxy-rules.json"] = "prepared"
	}
	p.point = &restorepoint.Point{Dir: p.temporary, Manifest: manifest}
	return p, nil
}

// ValidateConfigurationForUpdate checks the configuration that a replacement
// supervisor would start. It performs no lifecycle or network-setting writes.
// Call before disconnecting a live legacy core, which has no applied snapshot.
func ValidateConfigurationForUpdate(ctx context.Context, path string) error {
	dir := filepath.Dir(path)
	original := map[string]configurationInput{}
	for _, name := range []string{"sakamoto.yaml", "config.json"} {
		raw, err := configurationRead(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if !raw.exists {
			return errors.New("runtime configuration is missing")
		}
		original[name] = raw
	}
	if err := checkConfigurationPair(path, filepath.Join(dir, "config.json")); err != nil {
		return err
	}
	if _, _, err := dnsPlanFor(dir, filepath.Join(dir, "config.json")); err != nil {
		return err
	}
	if err := checkNativeConfiguration(ctx, singBoxExecutable(), dir, filepath.Join(dir, "config.json")); err != nil {
		return err
	}
	for name, want := range original {
		got, err := configurationRead(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if got.exists != want.exists || !bytes.Equal(got.raw, want.raw) {
			return errors.New("runtime configuration changed during validation")
		}
	}
	return nil
}

func checkNativeConfiguration(ctx context.Context, binary, dir, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "check", "-D", dir, "-c", path)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("native configuration check: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
func checkConfigurationPair(sidecar, core string) error {
	cfg, err := config.Load(sidecar)
	if err != nil {
		return err
	}
	if err = validateAPIService(core); err != nil {
		return err
	}
	raw, err := os.ReadFile(core)
	if err != nil {
		return err
	}
	var doc struct {
		Services []struct {
			Type, Listen, Secret string
			Port                 int `json:"listen_port"`
		} `json:"services"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	endpoint, err := url.Parse(cfg.API.URL)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil {
		return err
	}
	for _, service := range doc.Services {
		if service.Type == "api" && (service.Listen != endpoint.Hostname() || service.Port != port || service.Secret != cfg.API.Secret) {
			return errors.New("API identity differs between sidecar and core configuration; regenerate or restore a known-working configuration")
		}
	}
	return nil
}
func rebaseConfiguration(raw []byte, rulesDir, sourceDir, liveDir string) ([]byte, error) {
	return gen.RebaseConfiguration(raw, rulesDir, sourceDir, liveDir)
}
