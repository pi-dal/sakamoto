package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
)

func main() {
	cfgPath := os.Args[1]
	cfg, err := config.Load(cfgPath)
	if err != nil {
		panic(err)
	}
	dir := filepath.Dir(cfgPath)
	if err := gen.Run(gen.Options{ConfPath: cfg.ConfPath, SRJSONPath: cfg.SRJSONPath, NodesFile: cfg.NodesFile,
		AllowHosts: strings.Join(cfg.AllowHosts, ","), Cfg: cfg, OutDir: dir}); err != nil {
		panic(err)
	}
	// -D keeps runtime-relative rule paths (rules/*.srs) resolvable regardless
	// of the caller's cwd, matching the daemon/import directory contract.
	out, err := exec.Command("sing-box", "check", "-D", dir, "-c", filepath.Join(dir, "config.json")).CombinedOutput()
	if err != nil {
		panic(fmt.Errorf("sing-box check: %w: %s", err, out))
	}
	fmt.Println("generated + validated:", filepath.Join(dir, "config.json"))
}
