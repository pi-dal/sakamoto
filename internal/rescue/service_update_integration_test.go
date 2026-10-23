package rescue

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRepairServiceUpdateOSProcess(t *testing.T) {
	if os.Getenv("SAKAMOTO_SERVICE_UPDATE_OS") != "1" {
		return
	}
	index := 0
	for i, arg := range os.Args {
		if arg == "--" {
			index = i + 1
			break
		}
	}
	if index == 0 {
		os.Exit(2)
	}
	args := os.Args[index:]
	statePath := os.Getenv("SAKAMOTO_SERVICE_UPDATE_STATE")
	raw, err := os.ReadFile(statePath)
	rescueOK(t, err)
	var state map[string]any
	rescueOK(t, json.Unmarshal(raw, &state))
	write := func() { raw, _ := json.Marshal(state); rescueOK(t, os.WriteFile(statePath, raw, 0600)) }
	switch args[0] {
	case "/usr/bin/plutil":
		if args[1] == "-convert" && args[2] == "json" {
			data, e := os.ReadFile(args[len(args)-1])
			rescueOK(t, e)
			rescueWrite(t, os.Stdout, string(data))
			os.Exit(0)
		}
		if args[1] == "-convert" && args[2] == "xml1" {
			os.Exit(0)
		}
	case "/bin/launchctl":
		switch args[1] {
		case "print":
			if state["loaded"] == false || strings.Contains(args[2], "dev.pi-dal") {
				rescueWrite(t, os.Stderr, "Could not find service\n")
				os.Exit(113)
			}
			rescueWrite(t, os.Stdout, "state = running\nSAKAMOTO_DIR => "+state["runtime"].(string)+"\n")
			os.Exit(0)
		case "bootout":
			state["loaded"] = false
			state["bootouts"] = state["bootouts"].(float64) + 1
			write()
			os.Exit(0)
		case "bootstrap":
			data, e := os.ReadFile(args[len(args)-1])
			rescueOK(t, e)
			var plist struct {
				Arguments []string `json:"ProgramArguments"`
			}
			rescueOK(t, json.Unmarshal(data, &plist))
			fresh := !strings.HasSuffix(plist.Arguments[0], "sakamoto.previous")
			if fresh && state["fail_new"] == true {
				os.Exit(5)
			}
			state["loaded"] = true
			state["new"] = fresh
			write()
			os.Exit(0)
		}
	}
	os.Exit(3)
}

func TestRepairServiceUpdateRetainsExecutableAndRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			dir, err := os.MkdirTemp("/tmp", "service-update-")
			rescueOK(t, err)
			defer func() { _ = os.RemoveAll(dir) }()
			install := filepath.Join(dir, "system")
			rescueOK(t, os.Mkdir(install, 0755))
			oldBinary := filepath.Join(dir, "old-cli")
			newBinary := filepath.Join(dir, "new-cli")
			helper := filepath.Join(dir, "rescue")
			for path, data := range map[string]string{oldBinary: "old executable", newBinary: "new executable", helper: "independent rescue"} {
				rescueOK(t, os.WriteFile(path, []byte(data), 0755))
			}
			plistPath := filepath.Join(dir, "daemon.plist")
			plist, _ := json.Marshal(map[string]any{"Label": "dev.sakamoto.daemon", "ProgramArguments": []string{oldBinary, "daemon"}, "EnvironmentVariables": map[string]string{"SAKAMOTO_DIR": dir}})
			rescueOK(t, os.WriteFile(plistPath, plist, 0644))
			statePath := filepath.Join(dir, "os.json")
			state, _ := json.Marshal(map[string]any{"runtime": dir, "loaded": true, "new": false, "fail_new": fail, "bootouts": 0})
			rescueOK(t, os.WriteFile(statePath, state, 0600))
			var mu sync.Mutex
			socket, err := net.Listen("unix", filepath.Join(dir, "svc.sock"))
			rescueOK(t, err)
			defer func() { _ = socket.Close() }()
			go func() {
				for {
					conn, e := socket.Accept()
					if e != nil {
						return
					}
					func() {
						defer func() { _ = conn.Close() }()
						line, _ := bufio.NewReader(conn).ReadString('\n')
						mu.Lock()
						defer mu.Unlock()
						raw, _ := os.ReadFile(statePath)
						var current map[string]any
						_ = json.Unmarshal(raw, &current)
						reply := "disconnected"
						if strings.TrimSpace(line) == "info" {
							reply = "unknown: info"
							if current["new"] == true {
								installedRaw, e := os.ReadFile(plistPath)
								if e != nil {
									t.Error(e)
									return
								}
								var installed struct {
									Arguments []string `json:"ProgramArguments"`
								}
								if e = json.Unmarshal(installedRaw, &installed); e != nil {
									t.Error(e)
									return
								}
								identity, _ := json.Marshal(map[string]any{"capabilities": []string{"configuration-repair-v1"}, "supervisor": map[string]any{"executable": installed.Arguments[0], "pid": os.Getpid()}})
								reply = string(identity)
							}
						}
						_, _ = fmt.Fprintln(conn, reply)
					}()
				}
			}()
			sys := System{EUID: 0, UID: os.Getuid(), Run: func(ctx context.Context, command string, args ...string) (string, error) {
				mu.Lock()
				defer mu.Unlock()
				argv := append([]string{"-test.run=^TestRepairServiceUpdateOSProcess$", "--", command}, args...)
				process := exec.CommandContext(ctx, os.Args[0], argv...)
				process.Env = append(os.Environ(), "SAKAMOTO_SERVICE_UPDATE_OS=1", "SAKAMOTO_SERVICE_UPDATE_STATE="+statePath, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
				output, e := process.CombinedOutput()
				return string(output), e
			}}
			paths := ServiceUpdatePaths{Runtime: dir, Plist: plistPath, Install: install, Source: newBinary, RescueSource: helper}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			rescueOK(t, CheckServiceUpdate(ctx, paths, sys))
			rescueOK(t, StageServiceRescue(paths, sys))
			stagedState, e := os.ReadFile(statePath)
			rescueOK(t, e)
			var staged map[string]any
			rescueOK(t, json.Unmarshal(stagedState, &staged))
			if staged["loaded"] != true || staged["bootouts"] != float64(0) {
				t.Fatal("rescue staging changed the running service")
			}
			out, err := UpdateService(ctx, paths, sys)
			if fail && err == nil {
				t.Fatal("failed bootstrap was reported successful")
			}
			if !fail {
				rescueOK(t, err)
				if !out.Updated {
					t.Fatal("service not committed")
				}
			}
			copied, e := os.ReadFile(filepath.Join(out.Backup, "sakamoto.previous"))
			rescueOK(t, e)
			if string(copied) != "old executable" {
				t.Fatal("prior executable not retained")
			}
			data, e := os.ReadFile(plistPath)
			rescueOK(t, e)
			var installed struct {
				Arguments []string `json:"ProgramArguments"`
			}
			rescueOK(t, json.Unmarshal(data, &installed))
			if !strings.HasPrefix(installed.Arguments[0], install+string(filepath.Separator)) {
				t.Fatal("service still depends on the app/Homebrew path", installed.Arguments)
			}
			if fail && !strings.HasSuffix(installed.Arguments[0], "sakamoto.previous") {
				t.Fatal("failed replacement did not restore retained service")
			}
			_, e = os.Stat(filepath.Join(install, "rescue", "sakamoto-rescue"))
			rescueOK(t, e)
			// A wrong-runtime loaded job must be rejected without touching launchd.
			mu.Lock()
			raw, _ := os.ReadFile(statePath)
			var changed map[string]any
			_ = json.Unmarshal(raw, &changed)
			before := changed["bootouts"]
			changed["runtime"] = filepath.Join(dir, "foreign")
			raw, _ = json.Marshal(changed)
			rescueOK(t, os.WriteFile(statePath, raw, 0600))
			mu.Unlock()
			if CheckServiceUpdate(ctx, paths, sys) == nil {
				t.Fatal("foreign service accepted")
			}
			raw, _ = os.ReadFile(statePath)
			_ = json.Unmarshal(raw, &changed)
			if changed["bootouts"] != before {
				t.Fatal("inspection modified foreign launchd job")
			}
		})
	}
	if out := os.Getenv("SAKAMOTO_RECOVERY_ARTIFACT_DIR"); out != "" {
		rescueOK(t, os.MkdirAll(out, 0700))
		rescueOK(t, os.WriteFile(filepath.Join(out, "service-update.json"), []byte(`{"scenario":"controlled-service-update","source_and_previous_executables_retained":true,"independent_rescue_staged_first":true,"failed_bootstrap_restores_retained_service":true,"foreign_runtime_rejected_before_changes":true,"os_boundary":"isolated subprocess fixture","uses_real_launchd":false}`), 0600))
	}
}
