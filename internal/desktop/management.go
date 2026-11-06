package desktop

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/pi-dal/sakamoto/internal/gen"
	"github.com/pi-dal/sakamoto/pkg/mobileconf"
)

// Management is loaded only for explicit editing, never included in Snapshot.
// Raw node links and subscription URLs can contain credentials.
type ManualNode = gen.NodeEntry
type Management struct {
	Revision      string
	Nodes         []ManualNode
	Subscriptions []config.SubSource
	Policies      []config.PolicyRule
}
type LearningCheck struct {
	Domain, State, Detail string
	Time                  time.Time
}
type Learning struct {
	Mode         string
	LearnedCount int
	Checks       []LearningCheck
	Failures     []LearningCheck
}

func sourceRevision(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return fmt.Sprintf("%x", sha256.Sum256(nil)), nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
func (c *Controller) Management() (Management, error) {
	revision, err := sourceRevision(c.path)
	if err != nil {
		return Management{}, err
	}
	cfg, err := config.Load(c.path)
	if err != nil {
		return Management{}, err
	}
	nodes, err := gen.ReadNodes(cfg.NodesFile)
	if err != nil {
		return Management{}, err
	}
	after, err := sourceRevision(c.path)
	if err != nil {
		return Management{}, err
	}
	if revision != after {
		return Management{}, errors.New("configuration changed; reopen the editor")
	}
	return Management{Revision: revision, Nodes: nodes, Subscriptions: append([]config.SubSource(nil), cfg.Subscriptions...), Policies: append([]config.PolicyRule(nil), cfg.PolicyRules...)}, nil
}
func (c *Controller) saveCollection(ctx context.Context, name, revision string, apply func(*config.Config) error) error {
	return c.perform(ctx, name, func(ctx context.Context) error {
		current, err := sourceRevision(c.path)
		if err != nil {
			return err
		}
		if current != revision {
			return c.managementError("Configuration changed. Reopen the editor before saving.")
		}
		cfg, err := config.Load(c.path)
		if err != nil {
			return err
		}
		if err = apply(cfg); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = saveConfigurationAtomically(cfg, c.path); err != nil {
			return err
		}
		c.sourceSaved()
		return nil
	})
}
func (c *Controller) managementError(message string) error {
	c.update(func(s *Snapshot) { s.Notice = core.Errorf("%s", message) })
	return errors.New(message)
}
func (c *Controller) sourceSaved() {
	c.update(func(s *Snapshot) { s.Notice = core.Successf("Configuration saved. Regenerate to apply.") })
}
func (c *Controller) SaveNode(ctx context.Context, original ManualNode, replacement string) error {
	return c.perform(ctx, "Saving node", func(ctx context.Context) error {
		cfg, err := config.Load(c.path)
		if err != nil {
			return err
		}
		if cfg.NodesFile == "" {
			return c.managementError("Set a manual nodes file before adding nodes.")
		}
		if original.Line == 0 && strings.TrimSpace(replacement) == "" {
			return c.managementError("Paste a valid node share link.")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = gen.ChangeNode(cfg.NodesFile, original.Line, original.Raw, replacement); err != nil {
			return c.managementError("Node could not be saved. Check the link or reopen a changed node file.")
		}
		c.sourceSaved()
		return nil
	})
}
func (c *Controller) SaveSubscriptions(ctx context.Context, original Management, values []config.SubSource) error {
	return c.saveCollection(ctx, "Saving subscriptions", original.Revision, func(cfg *config.Config) error {
		clean := append([]config.SubSource(nil), values...)
		for i := range clean {
			v := &clean[i]
			v.Name = strings.TrimSpace(v.Name)
			v.URL = strings.TrimSpace(v.URL)
			u, err := url.Parse(v.URL)
			if v.Name == "" || strings.ContainsAny(v.Name, "\x00\r\n") || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || strings.ContainsAny(v.URL, "\x00\r\n") {
				return c.managementError("Enter a name and a valid HTTP(S) subscription URL.")
			}
			if v.Format == "" {
				v.Format = "auto"
			}
			switch v.Format {
			case "auto", "singbox", "clash", "base64":
			default:
				return c.managementError("Choose a supported subscription format.")
			}
		}
		cfg.Subscriptions = clean
		return nil
	})
}
func (c *Controller) SavePolicies(ctx context.Context, original Management, values []config.PolicyRule) error {
	return c.saveCollection(ctx, "Saving routing policy", original.Revision, func(cfg *config.Config) error {
		clean := append([]config.PolicyRule(nil), values...)
		seen := map[string]bool{}
		for i := range clean {
			v := &clean[i]
			v.Match = strings.TrimSpace(v.Match)
			v.Action = strings.ToLower(strings.TrimSpace(v.Action))
			match, kind, err := mobileconf.NormalizePolicyMatch(v.Match)
			if err != nil {
				return c.managementError("Enter a valid hostname, URL, suffix, keyword or CIDR.")
			}
			key := kind + "\x00" + match
			if seen[key] {
				return c.managementError("This match already has a policy rule. Edit the existing rule.")
			}
			seen[key] = true
		}
		cfg.PolicyRules = clean
		if err := cfg.ValidatePolicy(); err != nil {
			return c.managementError("Choose proxy, direct or reject for each policy rule.")
		}
		return nil
	})
}
func (c *Controller) Learning() (Learning, error) {
	cfg, err := config.Load(c.path)
	if err != nil {
		return Learning{}, err
	}
	dir := filepath.Dir(c.path)
	learned, err := experiment.Load(dir)
	if err != nil {
		return Learning{}, err
	}
	history, err := experiment.ReadReport(dir)
	if err != nil {
		return Learning{}, err
	}
	result := Learning{Mode: cfg.Experiment.Mode, LearnedCount: len(learned.Domains)}
	seen := map[string]bool{}
	for i := len(history) - 1; i >= 0 && i >= len(history)-20; i-- {
		v := history[i]
		domain := experiment.Domain(v.Domain)
		if domain == "" {
			continue
		}
		check := LearningCheck{Domain: domain, State: v.State, Detail: v.Detail, Time: v.Time}
		result.Checks = append(result.Checks, check)
		// The newest check for each domain wins, including a recovered direct path.
		if seen[domain] {
			continue
		}
		seen[domain] = true
		if !v.Direct.Reachable && v.Direct.Error != "" {
			result.Failures = append(result.Failures, check)
		}
	}
	return result, nil
}
func (c *Controller) AddRecentFailures(ctx context.Context, domains []string) error {
	return c.perform(ctx, "Adding recent failures", func(ctx context.Context) error {
		learning, err := c.Learning()
		if err != nil {
			return err
		}
		allowed := map[string]bool{}
		for _, v := range learning.Failures {
			allowed[v.Domain] = true
		}
		cfg, err := config.Load(c.path)
		if err != nil {
			return err
		}
		existing := map[string]bool{}
		for _, v := range cfg.PolicyRules {
			match, kind, e := mobileconf.NormalizePolicyMatch(v.Match)
			if e != nil {
				return e
			}
			if kind == "domain" {
				existing[match] = true
			}
		}
		added := 0
		for _, domain := range domains {
			if !allowed[domain] {
				return c.managementError("Recent checks changed. Refresh and select failed domains again.")
			}
			if existing[domain] {
				continue
			}
			cfg.PolicyRules = append(cfg.PolicyRules, config.PolicyRule{Match: domain, Action: "proxy"})
			existing[domain] = true
			added++
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if added > 0 {
			if err = saveConfigurationAtomically(cfg, c.path); err != nil {
				return err
			}
		}
		c.update(func(s *Snapshot) {
			s.Notice = core.Successf("%d proxy rules added; existing overrides kept. Regenerate to apply.", added)
		})
		return nil
	})
}
