package rescue

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Isolated launchd boundary: no owner jobs, TUN, DNS or proxy changes.
func TestWatcherUpgradeTransaction(t *testing.T) {
	for _, scenario := range []string{"success", "foreign", "bootstrap_failure", "canceled", "wrong_version"} {
		t.Run(scenario, func(t *testing.T) {
			dir, err := os.MkdirTemp("/tmp", "watcher-update-")
			rescueOK(t, err)
			defer func() { _ = os.RemoveAll(dir) }()
			runtimeDir := filepath.Join(dir, "runtime")
			rescueOK(t, os.Mkdir(runtimeDir, 0700))
			old := filepath.Join(dir, "old")
			source := filepath.Join(dir, "new")
			rescueOK(t, os.WriteFile(old, []byte("old watcher"), 0755))
			rescueOK(t, os.WriteFile(source, []byte("new watcher"), 0755))
			plistPath := filepath.Join(dir, "watch.plist")
			configured := runtimeDir
			if scenario == "foreign" {
				configured = filepath.Join(dir, "foreign")
			}
			raw, _ := json.Marshal(map[string]any{"Label": "dev.sakamoto.watch", "ProgramArguments": []string{old, "watch"}, "EnvironmentVariables": map[string]string{"SAKAMOTO_DIR": configured, "PATH": "/custom/bin"}})
			rescueOK(t, os.WriteFile(plistPath, raw, 0600))
			listener, e := net.Listen("unix", filepath.Join(runtimeDir, "watch.sock"))
			rescueOK(t, e)
			defer func() { _ = listener.Close() }()
			go func() {
				for {
					conn, e := listener.Accept()
					if e != nil {
						return
					}
					_, _ = bufio.NewReader(conn).ReadString('\n')
					v := "fixture"
					if scenario == "wrong_version" {
						v = "old"
					}
					raw, _ := json.Marshal(map[string]any{"version": v, "protocol": 2, "pid": 999})
					_, _ = fmt.Fprintln(conn, string(raw))
					_ = conn.Close()
				}
			}()
			loaded := true
			stops := 0
			replacement := ""
			sys := System{EUID: os.Geteuid(), UID: os.Getuid(), Run: func(ctx context.Context, command string, args ...string) (string, error) {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				if command == "/usr/bin/plutil" {
					if args[1] == "json" {
						data, e := os.ReadFile(args[len(args)-1])
						return string(data), e
					}
					return "", nil
				}
				switch args[0] {
				case "print":
					if !loaded {
						return "Could not find service", errors.New("missing")
					}
					return "state = running\npid = 999\nprogram = " + replacement + "\nSAKAMOTO_DIR => " + configured + "\n", nil
				case "bootout":
					loaded = false
					stops++
					return "", nil
				case "bootstrap":
					data, e := os.ReadFile(plistPath)
					if e != nil {
						return "", e
					}
					var p struct {
						Arguments []string `json:"ProgramArguments"`
					}
					if e = json.Unmarshal(data, &p); e != nil {
						return "", e
					}
					if p.Arguments[0] != old {
						replacement = p.Arguments[0]
						if scenario == "bootstrap_failure" {
							return "", errors.New("bootstrap failed")
						}
					}
					loaded = true
					return "", nil
				}
				return "", errors.New("unexpected command")
			}}
			paths := WatcherUpdatePaths{Runtime: runtimeDir, Plist: plistPath, Source: source, Install: filepath.Join(dir, "retained"), Version: "fixture"}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			err = UpdateWatcher(ctx, paths, sys)
			data, e := os.ReadFile(plistPath)
			rescueOK(t, e)
			if scenario == "success" {
				rescueOK(t, err)
				if !loaded || stops != 1 || replacement == "" || !strings.HasPrefix(replacement, paths.Install+string(filepath.Separator)) {
					t.Fatal("replacement was not retained and loaded")
				}
				got, e := os.ReadFile(replacement)
				rescueOK(t, e)
				if string(got) != "new watcher" {
					t.Fatal("wrong executable")
				}
				var p struct {
					Environment map[string]string `json:"EnvironmentVariables"`
				}
				rescueOK(t, json.Unmarshal(data, &p))
				if p.Environment["PATH"] != "/custom/bin" {
					t.Fatal("environment lost")
				}
			} else {
				if err == nil {
					t.Fatal("failure reported successful")
				}
				if string(data) != string(raw) || !loaded {
					t.Fatal("previous watcher not retained")
				}
				if (scenario == "foreign" || scenario == "canceled") && stops != 0 {
					t.Fatal("preflight failure stopped watcher")
				}
			}
		})
	}
	if dir := os.Getenv("SAKAMOTO_RECOVERY_ARTIFACT_DIR"); dir != "" {
		rescueOK(t, os.MkdirAll(dir, 0700))
		rescueOK(t, os.WriteFile(filepath.Join(dir, "watcher-update.json"), []byte(`{"passed":true,"scenarios":["success","foreign","bootstrap_failure","canceled","wrong_version"],"uses_real_launchd":false}`), 0600))
	}
}
