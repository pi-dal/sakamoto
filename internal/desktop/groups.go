package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
)

// AutomaticGroups exposes editable policy and public node names only. Revisions
// fence edits against both sidecar changes and a newly generated candidate set.
type AutomaticGroups struct {
	Revision, RuntimeRevision string
	Generated                 bool
	Groups                    []AutomaticGroup
}
type AutomaticGroup struct {
	Tag   string
	Nodes []AutomaticGroupNode
}
type AutomaticGroupNode struct {
	Tag               string
	Present, Excluded bool
}

func (c *Controller) AutomaticGroups() (AutomaticGroups, error) {
	revision, err := sourceRevision(c.path)
	if err != nil {
		return AutomaticGroups{}, err
	}
	cfg, err := config.Load(c.path)
	if err != nil {
		return AutomaticGroups{}, err
	}
	runtimePath := filepath.Join(filepath.Dir(c.path), "config.json")
	runtimeRevision, err := sourceRevision(runtimePath)
	if err != nil {
		return AutomaticGroups{}, err
	}
	raw, err := os.ReadFile(runtimePath)
	if err != nil && !os.IsNotExist(err) {
		return AutomaticGroups{}, err
	}
	result := AutomaticGroups{Revision: revision, RuntimeRevision: runtimeRevision, Generated: err == nil}
	var root struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if result.Generated {
		if err := json.Unmarshal(raw, &root); err != nil {
			return AutomaticGroups{}, errors.New("could not read generated proxy groups; regenerate configuration")
		}
	}
	// ManualPick is the generated entry-node set, including excluded nodes but
	// excluding chain exits. Do not infer candidates from live URL-test members.
	entries := map[string]bool{}
	for _, node := range root.Outbounds {
		if node["tag"] == "ManualPick" {
			members, _ := node["outbounds"].([]any)
			for _, value := range members {
				if tag, ok := value.(string); ok {
					entries[tag] = true
				}
			}
		}
	}
	for _, groupTag := range []string{"RealityAuto", "OthersAuto"} {
		group := AutomaticGroup{Tag: groupTag}
		seen := map[string]bool{}
		for _, node := range root.Outbounds {
			tag, _ := node["tag"].(string)
			category := gen.AutomaticGroupTag(node)
			if node["type"] == "socks" {
				category = "OthersAuto"
			}
			if !entries[tag] || category != groupTag || seen[tag] {
				continue
			}
			seen[tag] = true
			group.Nodes = append(group.Nodes, AutomaticGroupNode{Tag: tag, Present: true, Excluded: slices.Contains(cfg.AutomaticGroupExclusions[groupTag], tag)})
		}
		for _, tag := range cfg.AutomaticGroupExclusions[groupTag] {
			if !seen[tag] {
				group.Nodes = append(group.Nodes, AutomaticGroupNode{Tag: tag, Excluded: true})
				seen[tag] = true
			}
		}
		result.Groups = append(result.Groups, group)
	}
	after, err := sourceRevision(c.path)
	if err != nil {
		return AutomaticGroups{}, err
	}
	runtimeAfter, err := sourceRevision(runtimePath)
	if err != nil {
		return AutomaticGroups{}, err
	}
	if after != revision || runtimeAfter != runtimeRevision {
		return AutomaticGroups{}, errors.New("configuration changed; reopen proxy groups")
	}
	return result, nil
}

func (c *Controller) SaveAutomaticGroup(ctx context.Context, original AutomaticGroups, tag string, excluded []string) error {
	// Copy before dispatching so UI draft changes cannot alter an in-flight save.
	excluded = slices.Clone(excluded)
	return c.saveCollection(ctx, "Saving proxy group", original.Revision, func(cfg *config.Config) error {
		current, err := c.AutomaticGroups()
		if err != nil {
			return c.managementError("Could not read proxy groups. Regenerate configuration and reopen the editor.")
		}
		if current.Revision != original.Revision || current.RuntimeRevision != original.RuntimeRevision {
			return c.managementError("Configuration changed. Reopen proxy groups before saving.")
		}
		var group *AutomaticGroup
		for i := range current.Groups {
			if current.Groups[i].Tag == tag {
				group = &current.Groups[i]
				break
			}
		}
		if group == nil {
			return c.managementError("Choose RealityAuto or OthersAuto.")
		}
		known := map[string]bool{}
		present := 0
		for _, node := range group.Nodes {
			known[node.Tag] = true
			if node.Present {
				present++
			}
		}
		seen := map[string]bool{}
		for _, node := range excluded {
			if !known[node] || seen[node] {
				return c.managementError("Node list changed or is invalid. Reopen proxy groups before saving.")
			}
			seen[node] = true
		}
		included := 0
		for _, node := range group.Nodes {
			if node.Present && !seen[node.Tag] {
				included++
			}
		}
		if present > 0 && included == 0 {
			return c.managementError("Keep at least one available node included.")
		}
		if cfg.AutomaticGroupExclusions == nil {
			cfg.AutomaticGroupExclusions = map[string][]string{}
		}
		cfg.AutomaticGroupExclusions[tag] = excluded
		return cfg.ValidateAutomaticGroups()
	})
}
