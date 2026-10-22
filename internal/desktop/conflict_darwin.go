//go:build darwin

package desktop

import (
	"context"
	"os/exec"
	"strings"
)

// ShadowrocketConnected only observes conflicts; the supervisor enforces them.
func ShadowrocketConnected(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--nc", "list").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "(Connected)") && strings.Contains(line, "com.liguangming.Shadowrocket") {
			return true
		}
	}
	return false
}
