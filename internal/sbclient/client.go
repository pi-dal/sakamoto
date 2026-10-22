package sbclient

import (
	"context"
	"fmt"
	"time"

	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Client wraps sing-box 1.14's native gRPC API service.
type Client struct {
	conn *grpc.ClientConn
	svc  daemon.StartedServiceClient
}

func Dial(ctx context.Context, serverURL, secret string) (*Client, error) {
	conn, err := daemon.NewRemoteClient(daemon.RemoteClientOptions{
		ServerURL: serverURL,
		Secret:    secret,
	})
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, svc: daemon.NewStartedServiceClient(conn)}
	if _, err := c.svc.GetVersion(ctx, &emptypb.Empty{}); err != nil {
		_ = conn.Close() // original dial error takes precedence
		return nil, fmt.Errorf("connect %s: %w", serverURL, err)
	}
	return c, nil
}

func (c *Client) Close() { _ = c.conn.Close() }

// StartedAt identifies the running core and guards on-disk configuration edits.
func (c *Client) StartedAt(ctx context.Context) (time.Time, error) {
	value, err := c.svc.GetStartedAt(ctx, &emptypb.Empty{})
	if err != nil {
		return time.Time{}, err
	}
	if value.StartedAt <= 0 {
		return time.Time{}, fmt.Errorf("core start time unavailable")
	}
	return time.UnixMilli(value.StartedAt), nil
}

func (c *Client) Version(ctx context.Context) (string, error) {
	v, err := c.svc.GetVersion(ctx, &emptypb.Empty{})
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

// SelectOutbound changes a selector group's active outbound.
func (c *Client) SelectOutbound(ctx context.Context, groupTag, outboundTag string) error {
	_, err := c.svc.SelectOutbound(ctx, &daemon.SelectOutboundRequest{
		GroupTag:    groupTag,
		OutboundTag: outboundTag,
	})
	return err
}

// URLTest triggers a latency check for a group or single node.
func (c *Client) URLTest(ctx context.Context, tag string) error {
	_, err := c.svc.URLTest(ctx, &daemon.URLTestRequest{OutboundTag: tag})
	return err
}

// SubscribeGroups streams group snapshots; callers reconnect on stream errors.
func (c *Client) SubscribeGroups(ctx context.Context) (<-chan *daemon.Groups, <-chan error) {
	ch := make(chan *daemon.Groups, 8)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		stream, err := c.svc.SubscribeGroups(ctx, &emptypb.Empty{})
		if err != nil {
			errCh <- err
			return
		}
		for {
			g, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			select {
			case ch <- g:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, errCh
}

// SetClashMode selects rule/global/direct routing mode.
func (c *Client) SetClashMode(ctx context.Context, mode string) error {
	_, err := c.svc.SetClashMode(ctx, &daemon.ClashMode{Mode: mode})
	return err
}

// ClashModeStatus reports available modes and the current selection.
func (c *Client) ClashModeStatus(ctx context.Context) (*daemon.ClashModeStatus, error) {
	return c.svc.GetClashModeStatus(ctx, &emptypb.Empty{})
}

// SubscribeClashMode keeps the TUI synchronized with changes from the native
// API, including mode switches made outside the current TUI session.
func (c *Client) SubscribeClashMode(ctx context.Context) (<-chan *daemon.ClashMode, <-chan error) {
	ch := make(chan *daemon.ClashMode, 8)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		stream, err := c.svc.SubscribeClashMode(ctx, &emptypb.Empty{})
		if err != nil {
			errCh <- err
			return
		}
		for {
			mode, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			select {
			case ch <- mode:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, errCh
}

// SubscribeLog streams sing-box service log messages.
func (c *Client) SubscribeLog(ctx context.Context) (<-chan *daemon.Log, <-chan error) {
	ch := make(chan *daemon.Log, 32)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		stream, err := c.svc.SubscribeLog(ctx, &emptypb.Empty{})
		if err != nil {
			errCh <- err
			return
		}
		for {
			l, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			select {
			case ch <- l:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, errCh
}

// CloseConnection terminates one active connection on explicit user request.
func (c *Client) CloseConnection(ctx context.Context, id string) error {
	_, err := c.svc.CloseConnection(ctx, &daemon.CloseConnectionRequest{Id: id})
	return err
}

// SubscribeConnections streams lifecycle events immediately and traffic deltas
// at the given duration. A one-second floor prevents accidental busy polling.
func (c *Client) SubscribeConnections(ctx context.Context, interval time.Duration) (<-chan *daemon.ConnectionEvents, <-chan error) {
	ch := make(chan *daemon.ConnectionEvents, 8)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		stream, err := c.svc.SubscribeConnections(ctx, &daemon.SubscribeConnectionsRequest{Interval: int64(max(interval, time.Second))})
		if err != nil {
			errCh <- err
			return
		}
		for {
			ev, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, errCh
}

// SubscribeStatus streams metrics with a one-second minimum duration.
// The native wire representation is nanoseconds, just like time.Duration.
func (c *Client) SubscribeStatus(ctx context.Context, interval time.Duration) (<-chan *daemon.Status, <-chan error) {
	ch := make(chan *daemon.Status, 8)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		stream, err := c.svc.SubscribeStatus(ctx, &daemon.SubscribeStatusRequest{Interval: int64(max(interval, time.Second))})
		if err != nil {
			errCh <- err
			return
		}
		for {
			s, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			select {
			case ch <- s:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, errCh
}
