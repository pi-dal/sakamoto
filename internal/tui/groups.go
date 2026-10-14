package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
)

var automaticGroupTags = []string{"RealityAuto", "OthersAuto"}

func automaticGroupNodes(raw []byte, group string) ([]string, error) {
	var root struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("read generated configuration: %w", err)
	}
	var names []string
	for _, node := range root.Outbounds {
		if gen.AutomaticGroupTag(node) == group {
			if tag, ok := node["tag"].(string); ok && tag != "" {
				names = append(names, tag)
			}
		}
	}
	return names, nil
}

func (m *model) editAutomaticGroup(index int) {
	if index < 0 || index >= len(automaticGroupTags) {
		return
	}
	group := automaticGroupTags[index]
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(m.cfgPath), "config.json"))
	if err != nil {
		m.notice = "Generate a configuration before choosing group members"
		return
	}
	names, err := automaticGroupNodes(raw, group)
	if err != nil {
		m.notice = err.Error()
		return
	}
	cfg, err := config.Load(m.cfgPath)
	if err != nil {
		m.notice = "Could not load saved settings: " + err.Error()
		return
	}
	baseline, err := os.ReadFile(m.cfgPath)
	if err != nil {
		m.notice = "Save settings before choosing group members"
		return
	}
	if cfg.AutomaticGroupExclusions == nil {
		cfg.AutomaticGroupExclusions = map[string][]string{}
	}
	present := slices.Clone(names)
	for _, tag := range cfg.AutomaticGroupExclusions[group] {
		if !slices.Contains(names, tag) {
			names = append(names, tag)
		}
	}
	m.formCfg = cfg
	m.formGroup = group
	m.formConfigSnapshot = string(baseline)
	m.formRuntimeSnapshot = string(raw)
	m.formRows = nil
	for _, tag := range names {
		label := tag
		if !slices.Contains(present, tag) {
			label += " (currently absent)"
		}
		m.formRows = append(m.formRows, cfgRow{label: label, value: func() string {
			if slices.Contains(cfg.AutomaticGroupExclusions[group], tag) {
				return "Excluded"
			}
			return "Included"
		}, toggle: func() {
			excluded := cfg.AutomaticGroupExclusions[group]
			if slices.Contains(excluded, tag) {
				cfg.AutomaticGroupExclusions[group] = slices.DeleteFunc(excluded, func(name string) bool { return name == tag })
			} else {
				cfg.AutomaticGroupExclusions[group] = append(excluded, tag)
			}
		}})
	}
	m.configForm, m.formEditing = true, false
	m.formCursor, m.formScroll, m.formEditIndex = 0, 0, -1
	m.formNotice = "New nodes join; exclusions survive regeneration."
	if len(present) == 0 {
		m.formNotice = "No matching nodes yet; add sources and regenerate."
	}
}

func (m *model) validateGroupForm() error {
	settings, err := os.ReadFile(m.cfgPath)
	if err != nil {
		return err
	}
	runtime, err := os.ReadFile(filepath.Join(filepath.Dir(m.cfgPath), "config.json"))
	if err != nil {
		return err
	}
	if !bytes.Equal(settings, []byte(m.formConfigSnapshot)) || !bytes.Equal(runtime, []byte(m.formRuntimeSnapshot)) {
		return fmt.Errorf("configuration changed; cancel and reopen the group")
	}
	names, err := automaticGroupNodes(runtime, m.formGroup)
	if err != nil {
		return err
	}
	if len(names) > 0 {
		for _, name := range names {
			if !slices.Contains(m.formCfg.AutomaticGroupExclusions[m.formGroup], name) {
				return nil
			}
		}
		return fmt.Errorf("keep at least one available member in %s", m.formGroup)
	}
	return nil
}

func (m *model) renderAutomaticGroups(b *strings.Builder, startY int) {
	w := newContentWriter(m, b, startY)
	w.line(" " + accent.Render("Proxy groups"))
	w.line(" Choose which nodes join automatic latency testing.")
	for i, group := range automaticGroupTags {
		w.row(fmt.Sprintf("   %s  ·  %d excluded  ›", group, len(m.cfg.AutomaticGroupExclusions[group])), "edit-auto-group", i, i == m.groupCursor)
	}
	w.line(" New nodes join; saved exclusions stay out.")
	w.line(" ManualPick keeps every node for manual selection.")
	w.buttons(control{text: "[ Back to Config ]", action: "back"})
	w.line(" Enter edits · ↑↓ chooses · Esc goes back")
}
