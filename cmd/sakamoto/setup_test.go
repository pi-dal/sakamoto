package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupFixture(t *testing.T) setupPaths {
	t.Helper()
	// Darwin has a short Unix socket path limit; avoid verbose t.TempDir paths.
	root, err := os.MkdirTemp("/tmp", "sakamoto-setup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	paths := setupPaths{
		configPath: filepath.Join(root, "state", "sakamoto.yaml"),
		runtimeDir: filepath.Join(root, "state"),
		home:       filepath.Join(root, "home"),
		daemonDir:  filepath.Join(root, "daemons"),
	}
	for _, dir := range []string{paths.runtimeDir, paths.daemonDir, filepath.Join(paths.home, "Library", "LaunchAgents")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func setupPlist(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("synthetic plist\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSetupPreflightRefusesPartialOrStaleInstall(t *testing.T) {
	p := setupFixture(t)
	if state, err := inspectSetup(p); err != nil || state.installed {
		t.Fatalf("fresh directory was not clean: %+v %v", state, err)
	}
	rootPlist := filepath.Join(p.daemonDir, "dev.sakamoto.daemon.plist")
	watchPlist := filepath.Join(p.home, "Library", "LaunchAgents", "dev.sakamoto.watch.plist")
	setupPlist(t, rootPlist)
	if _, err := inspectSetup(p); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatal("partial root installation was accepted", err)
	}
	setupPlist(t, watchPlist)
	if _, err := inspectSetup(p); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatal("unavailable root service was silently replaced", err)
	}
	if err := os.Remove(rootPlist); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(watchPlist); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(p.runtimeDir, "svc.sock")
	if err := os.WriteFile(stale, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectSetup(p); err == nil || !strings.Contains(err.Error(), "socket exists") {
		t.Fatal("orphaned supervisor socket was ignored", err)
	}
}

func TestSetupExistingSupervisorIsReadOnly(t *testing.T) {
	p := setupFixture(t)
	rootPlist := filepath.Join(p.daemonDir, "dev.pi-dal.sing-box.plist")
	watchPlist := filepath.Join(p.home, "Library", "LaunchAgents", "dev.pi-dal.sakamoto-watch.plist")
	setupPlist(t, rootPlist)
	setupPlist(t, watchPlist)
	listener, err := net.Listen("unix", filepath.Join(p.runtimeDir, "svc.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		command, _ := bufio.NewReader(conn).ReadString('\n')
		if command == "status\n" {
			_, _ = conn.Write([]byte("connected pid=123 dns=protected\n"))
		}
	}()
	state, err := inspectSetup(p)
	_ = listener.Close() // Also unblocks Accept if inspection returned before dialing.
	<-finished
	if err != nil || !state.installed || state.label != "dev.pi-dal.sing-box" || state.watcher != "dev.pi-dal.sakamoto-watch" || !strings.Contains(state.status, "dns=protected") {
		t.Fatalf("installed service was not reported safely: %+v %v", state, err)
	}
	for _, path := range []string{rootPlist, watchPlist} {
		if raw, err := os.ReadFile(path); err != nil || string(raw) != "synthetic plist\n" {
			t.Fatalf("setup inspection modified a plist: %s %v", path, err)
		}
	}
}

func TestSetupScriptFindsSourceAndHomebrewLayout(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name, bin, pkgRoot string
	}{
		{"source", filepath.Join(root, "source", "sakamoto"), filepath.Join(root, "source")},
		{"homebrew", filepath.Join(root, "Cellar", "sakamoto", "0.2.6", "bin", "sakamoto"), filepath.Join(root, "Cellar", "sakamoto", "0.2.6", "share", "sakamoto")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Dir(tc.bin), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(tc.bin, nil, 0700); err != nil {
				t.Fatal(err)
			}
			for _, part := range []string{"scripts", "launchd"} {
				if err := os.MkdirAll(filepath.Join(tc.pkgRoot, part), 0700); err != nil {
					t.Fatal(err)
				}
			}
			setupPlist(t, filepath.Join(tc.pkgRoot, "scripts", "install-macos.sh"))
			setupPlist(t, filepath.Join(tc.pkgRoot, "launchd", "dev.sakamoto.daemon.plist.in"))
			setupPlist(t, filepath.Join(tc.pkgRoot, "launchd", "dev.sakamoto.watch.plist.in"))
			binary := tc.bin
			if tc.name == "homebrew" {
				link := filepath.Join(root, "bin", "sakamoto")
				if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(tc.bin, link); err != nil {
					t.Fatal(err)
				}
				binary = link
			}
			script, err := setupScript(binary)
			want, wantErr := filepath.EvalSymlinks(filepath.Join(tc.pkgRoot, "scripts", "install-macos.sh"))
			if err != nil || wantErr != nil || script != want {
				t.Fatalf("setup resource discovery: %q, want %q (%v / %v)", script, want, err, wantErr)
			}
		})
	}
}
