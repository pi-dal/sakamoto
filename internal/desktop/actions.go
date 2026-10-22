// Package desktop provides UI-independent macOS controller workflows.
package desktop

import (
	"context"
	"fmt"
	"strings"

	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/sagernet/sing-box/daemon"
)

// SelectNode preserves the watcher's manual-selection boundary for auto groups.
func SelectNode(ctx context.Context, client *sbclient.Client, groups []*daemon.Group, groupTag, tag string) error {
	var selected *daemon.Group
	for _, group := range groups {
		if group.Tag == groupTag {
			selected = group
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("group is no longer available; refresh and try again")
	}
	found := false
	for _, item := range selected.Items {
		if item.Tag == tag {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("node is no longer available; refresh and try again")
	}
	if selected.Selectable {
		return client.SelectOutbound(ctx, groupTag, tag)
	}
	for _, group := range groups {
		if group.Tag != "ManualPick" || !group.Selectable {
			continue
		}
		for _, item := range group.Items {
			if item.Tag != tag {
				continue
			}
			if err := client.SelectOutbound(ctx, "ManualPick", tag); err != nil {
				return err
			}
			return client.SelectOutbound(ctx, "MainProxy", "ManualPick")
		}
	}
	return fmt.Errorf("this node is unavailable in ManualPick")
}

// ApplyMode validates the live capability and verifies the observed result.
func ApplyMode(ctx context.Context, client *sbclient.Client, requested string) (string, error) {
	mode, err := core.ParseRoutingMode(requested)
	if err != nil {
		return "", err
	}
	before, err := client.ClashModeStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("read routing mode: %w", err)
	}
	available := ""
	for _, value := range before.ModeList {
		if strings.EqualFold(value, string(mode)) {
			available = value
		}
	}
	if available == "" {
		return "", fmt.Errorf("%s mode is unavailable; regenerate and reconnect", mode)
	}
	if err := client.SetClashMode(ctx, available); err != nil {
		return "", fmt.Errorf("set routing mode: %w", err)
	}
	after, err := client.ClashModeStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("verify routing mode: %w", err)
	}
	if !strings.EqualFold(after.CurrentMode, string(mode)) {
		return "", fmt.Errorf("routing mode was not applied; refresh and try again")
	}
	return after.CurrentMode, nil
}
