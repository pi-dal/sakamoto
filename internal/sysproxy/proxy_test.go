package sysproxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

func TestSyncRestoresOriginalProxy(t *testing.T) {
	cfg := config.Default()
	cfg.SystemProxy.Enabled = true
	cfg.SystemProxy.Service = "Wi-Fi"
	cfg.MixedInbound.Enabled = true
	cfg.MixedInbound.Port = 2334
	state := filepath.Join(t.TempDir(), "proxy-restore.json")
	current := map[string]Proxy{"web": {Server: "127.0.0.1", Port: 8899}, "secureweb": {Server: "127.0.0.1", Port: 8899}}
	runner := func(args ...string) (string, error) {
		kind := "web"
		if strings.Contains(args[0], "secureweb") {
			kind = "secureweb"
		}
		p := current[kind]
		switch args[0] {
		case "-getwebproxy", "-getsecurewebproxy":
			enabled := "No"
			if p.Enabled {
				enabled = "Yes"
			}
			return fmt.Sprintf("Enabled: %s\nServer: %s\nPort: %d\nAuthenticated Proxy Enabled: 0\n", enabled, p.Server, p.Port), nil
		case "-setwebproxy", "-setsecurewebproxy":
			p.Server = args[2]
			p.Port, _ = strconv.Atoi(args[3])
		case "-setwebproxystate", "-setsecurewebproxystate":
			p.Enabled = args[2] == "on"
		default:
			return "", fmt.Errorf("unexpected command %s", args[0])
		}
		current[kind] = p
		return "", nil
	}
	if err := Sync(state, cfg, true, runner); err != nil {
		t.Fatal(err)
	}
	if !current["web"].Enabled || current["web"].Port != 2334 || !current["secureweb"].Enabled {
		t.Fatal("proxy was not enabled")
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal("restore state missing")
	}
	if err := Sync(state, cfg, true, runner); err != nil {
		t.Fatal(err)
	} // idempotent
	if err := Sync(state, cfg, false, runner); err != nil {
		t.Fatal(err)
	}
	if current["web"].Enabled || current["web"].Port != 8899 || current["secureweb"].Enabled {
		t.Fatal("original proxy was not restored")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("restore state not removed", err)
	}
}
