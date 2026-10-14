package sourcesync

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const GroupsFile = "groups.json"

// Groups is portable automatic-group policy, independent of generated members,
// device-local node aliases, chain settings and credentials.
type Groups map[string][]string

func (g Groups) Validate() error {
	for group, tags := range g {
		if group != "RealityAuto" && group != "OthersAuto" {
			return fmt.Errorf("automatic_group_exclusions: unknown group %q", group)
		}
		seen := map[string]bool{}
		for _, tag := range tags {
			if strings.TrimSpace(tag) == "" || strings.ContainsAny(tag, "\x00\r\n") || seen[tag] {
				return fmt.Errorf("automatic_group_exclusions.%s must contain distinct non-empty node tags", group)
			}
			seen[tag] = true
		}
	}
	return nil
}

func DecodeGroups(raw string) (Groups, error) {
	var groups Groups
	if json.Unmarshal([]byte(raw), &groups) != nil || groups == nil {
		return nil, fmt.Errorf("invalid automatic group source")
	}
	for _, tags := range groups {
		if tags == nil {
			return nil, fmt.Errorf("automatic group exclusions must be arrays")
		}
	}
	return groups, groups.Validate()
}

func (g Groups) JSON() (string, error) {
	if err := g.Validate(); err != nil {
		return "", err
	}
	canonical := Groups{}
	for group, tags := range g {
		if len(tags) == 0 {
			continue
		}
		ordered := append([]string{}, tags...)
		slices.Sort(ordered)
		canonical[group] = ordered
	}
	raw, err := json.Marshal(canonical)
	return string(raw), err
}
