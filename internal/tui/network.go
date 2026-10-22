package tui

import (
	"context"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/desktop"
)

func diagnoseNetwork(cfg *config.Config) networkMsg {
	outcome := desktop.ProbeNetwork(context.Background(), cfg, nil)
	return networkMsg{path: outcome.Path, err: outcome.Err}
}
