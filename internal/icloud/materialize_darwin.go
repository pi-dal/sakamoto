//go:build darwin

package icloud

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func sourceIsDataless(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Flags&0x40000000 != 0 // Darwin UF_DATALESS
}

func requestSourceDownload(path string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	// Request materialization and retry on the next pass; never wait for a
	// download to complete or read placeholder contents during preflight.
	command := exec.CommandContext(ctx, "/usr/bin/brctl", "download", path)
	if err := command.Start(); err != nil {
		cancel()
		return
	}
	go func() { defer cancel(); _ = command.Wait() }()
}
