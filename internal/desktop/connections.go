package desktop

import (
	"context"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ConnectionState string

const (
	ConnectionsLoading     ConnectionState = "loading"
	ConnectionsLive        ConnectionState = "live"
	ConnectionsRetrying    ConnectionState = "retrying"
	ConnectionsUnsupported ConnectionState = "unsupported"
)

// Connection is an ephemeral, read-only projection. Full process paths, users
// and source addresses are deliberately excluded from the desktop view.
type Connection struct {
	ID, Target, Destination, Process, Network, Outbound string
	CreatedAt, UplinkTotal, DownlinkTotal               int64
}

func clearConnections(s *Snapshot) { s.Connections = nil; s.ConnectionsState = "" }

// SetConnectionsVisible scopes the high-volume stream to its page. A generation
// prevents a cancelled stream from publishing after rapid back/forward navigation.
func (c *Controller) SetConnectionsVisible(visible bool) {
	c.mu.Lock()
	if c.connectionsVisible == visible {
		c.mu.Unlock()
		return
	}
	c.connectionsVisible = visible
	c.connectionsGeneration++
	clearConnections(&c.state)
	close(c.connectionsChanged)
	c.connectionsChanged = make(chan struct{})
	c.mu.Unlock()
	select {
	case c.changes <- struct{}{}:
	default:
	}
}

func (c *Controller) updateConnections(epoch, generation uint64, state ConnectionState, values []Connection) {
	c.update(func(s *Snapshot) {
		if c.epoch == epoch && c.connectionsGeneration == generation && c.visible && c.connectionsVisible && s.Service == core.ServiceRunning {
			s.ConnectionsState = state
			s.Connections = values
		}
	})
}

func (c *Controller) watchConnections(ctx context.Context, cfg *config.Config, epoch uint64) {
	for ctx.Err() == nil {
		c.mu.Lock()
		visible, changed, generation := c.connectionsVisible, c.connectionsChanged, c.connectionsGeneration
		c.mu.Unlock()
		if !visible {
			select {
			case <-ctx.Done():
				return
			case <-changed:
				continue
			}
		}
		c.updateConnections(epoch, generation, ConnectionsLoading, nil)
		streamCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- c.streamConnections(streamCtx, cfg, epoch, generation) }()
		var err error
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return
		case <-changed:
			cancel()
			<-done
			continue
		case err = <-done:
			cancel()
		}
		state := ConnectionsRetrying
		if status.Code(err) == codes.Unimplemented {
			state = ConnectionsUnsupported
		}
		c.updateConnections(epoch, generation, state, nil)
		if state == ConnectionsUnsupported {
			select {
			case <-ctx.Done():
				return
			case <-changed:
			}
		} else {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-changed:
				timer.Stop()
			case <-timer.C:
			}
		}
	}
}

func (c *Controller) streamConnections(ctx context.Context, cfg *config.Config, epoch, generation uint64) error {
	dialCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	client, err := sbclient.Dial(dialCtx, cfg.API.URL, cfg.API.Secret)
	cancel()
	if err != nil {
		return err
	}
	defer client.Close()
	values, failures := client.SubscribeConnections(ctx, 2*time.Second)
	active := map[string]Connection{}
	// Lifecycle bursts can contain thousands of individual events. Fold them
	// immediately, but sort/copy at most ten times a second for the panel.
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var pending <-chan time.Time
	publish := func() {
		snapshot := make([]Connection, 0, len(active))
		for _, v := range active {
			snapshot = append(snapshot, v)
		}
		sort.Slice(snapshot, func(i, j int) bool {
			if snapshot[i].CreatedAt == snapshot[j].CreatedAt {
				return snapshot[i].ID < snapshot[j].ID
			}
			return snapshot[i].CreatedAt > snapshot[j].CreatedAt
		})
		c.updateConnections(epoch, generation, ConnectionsLive, snapshot)
	}
	for {
		select {
		case <-pending:
			publish()
			pending = nil
		case <-ctx.Done():
			return ctx.Err()
		case err := <-failures:
			return err
		case value, ok := <-values:
			if !ok {
				// The client closes data after sending its error. Preserve that error to
				// distinguish unsupported tracking from a recoverable stream interruption.
				select {
				case err := <-failures:
					if err != nil {
						return err
					}
				case <-ctx.Done():
					return ctx.Err()
				}
				return io.EOF
			}
			if value == nil {
				continue
			}
			if value.Reset_ {
				clear(active)
			}
			for _, event := range value.Events {
				if event == nil || event.Id == "" {
					continue
				}
				if event.Type == daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED || event.ClosedAt > 0 || event.GetConnection().GetClosedAt() > 0 {
					delete(active, event.Id)
					continue
				}
				if v := event.Connection; v != nil {
					target := v.Domain
					if target == "" {
						target = v.Destination
					}
					if target == "" {
						target = "Unknown destination"
					}
					process := ""
					if v.ProcessInfo != nil && v.ProcessInfo.ProcessPath != "" {
						process = filepath.Base(strings.ReplaceAll(v.ProcessInfo.ProcessPath, `\`, "/"))
					}
					active[event.Id] = Connection{ID: event.Id, Target: target, Destination: v.Destination, Process: process, Network: v.Network, Outbound: v.Outbound, CreatedAt: v.CreatedAt, UplinkTotal: max(0, v.UplinkTotal), DownlinkTotal: max(0, v.DownlinkTotal)}
				} else if event.Type == daemon.ConnectionEventType_CONNECTION_EVENT_UPDATE {
					if v, exists := active[event.Id]; exists {
						v.UplinkTotal += max(0, event.UplinkDelta)
						v.DownlinkTotal += max(0, event.DownlinkDelta)
						active[event.Id] = v
					}
				}
			}
			if value.Reset_ {
				publish()
				timer.Stop()
				pending = nil
			} else if pending == nil {
				timer.Reset(100 * time.Millisecond)
				pending = timer.C
			}
		}
	}
}
