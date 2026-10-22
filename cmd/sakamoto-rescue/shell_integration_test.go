package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRescueShellProcess(t *testing.T) {
	if os.Getenv("SAKAMOTO_RESCUE_SHELL_TEST") != "1" {
		return
	}
	os.Args = []string{"sakamoto-rescue", "shell"}
	if err := run(); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestRescueShellBypassesProxyStartupFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("clean rescue shell deliberately refuses root")
	}
	home := t.TempDir()
	poison := []byte("export http_proxy=http://127.0.0.1:1\nexport SHELL_STARTUP=loaded\n")
	for _, name := range []string{".zshenv", ".zshrc", ".bashrc", ".bash_profile", "inherited-startup"} {
		if err := os.WriteFile(filepath.Join(home, name), poison, 0600); err != nil {
			t.Fatal(err)
		}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestRescueShellProcess$")
	child.Env = append(os.Environ(), "SAKAMOTO_RESCUE_SHELL_TEST=1", "HOME="+home, "ZDOTDIR="+home, "BASH_ENV="+filepath.Join(home, "inherited-startup"), "ENV="+filepath.Join(home, "inherited-startup"), "http_proxy=http://127.0.0.1:2", "HTTPS_PROXY=http://127.0.0.1:2", "ALL_PROXY=http://127.0.0.1:2")
	child.Stdin = bytes.NewBufferString("printf 'proxy=%s|https=%s|all=%s|startup=%s\\n' \"${http_proxy-unset}\" \"${HTTPS_PROXY-unset}\" \"${ALL_PROXY-unset}\" \"${SHELL_STARTUP-unset}\"\nexit\n")
	output, err := child.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "proxy=unset|https=unset|all=unset|startup=unset") {
		t.Fatal("rescue shell inherited broken proxy or startup file", string(output), err)
	}
	if dir := os.Getenv("SAKAMOTO_RECOVERY_ARTIFACT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, t.Name()+".json"), []byte(`{"scenario":"clean-shell","proxy_environment_removed":true,"startup_files_bypassed":true}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
