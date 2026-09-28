package sbclient

import (
	"context"
	"fmt"

	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Client 封装 sing-box 1.14 原生 gRPC api service。
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
		conn.Close()
		return nil, fmt.Errorf("connect %s: %w", serverURL, err)
	}
	return c, nil
}

func (c *Client) Close() { c.conn.Close() }

func (c *Client) Version(ctx context.Context) (string, error) {
	v, err := c.svc.GetVersion(ctx, &emptypb.Empty{})
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

// SelectOutbound 切换 selector 组选中项。
func (c *Client) SelectOutbound(ctx context.Context, groupTag, outboundTag string) error {
	_, err := c.svc.SelectOutbound(ctx, &daemon.SelectOutboundRequest{
		GroupTag:    groupTag,
		OutboundTag: outboundTag,
	})
	return err
}

// URLTest 触发一次延迟测试（组或单节点）。
func (c *Client) URLTest(ctx context.Context, tag string) error {
	_, err := c.svc.URLTest(ctx, &daemon.URLTestRequest{OutboundTag: tag})
	return err
}

// SubscribeGroups 持续推送组状态快照；断流时返回 error，由调用方重连。
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

// SetClashMode 切换 rule/global/direct 模式（对齐 SR GlobalRoutingMethod）。
func (c *Client) SetClashMode(ctx context.Context, mode string) error {
	_, err := c.svc.SetClashMode(ctx, &daemon.ClashMode{Mode: mode})
	return err
}

// ClashModeStatus 返回模式列表与当前模式。
func (c *Client) ClashModeStatus(ctx context.Context) (*daemon.ClashModeStatus, error) {
	return c.svc.GetClashModeStatus(ctx, &emptypb.Empty{})
}

// SubscribeLog 推送服务日志流（等价 sing-box 控制台输出）。
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

// CloseConnection 关闭一条活动连接（Data 页显式操作）。
func (c *Client) CloseConnection(ctx context.Context, id string) error {
	_, err := c.svc.CloseConnection(ctx, &daemon.CloseConnectionRequest{Id: id})
	return err
}

// SubscribeConnections 推送连接事件流。
func (c *Client) SubscribeConnections(ctx context.Context, intervalMs int64) (<-chan *daemon.ConnectionEvents, <-chan error) {
	ch := make(chan *daemon.ConnectionEvents, 8)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		stream, err := c.svc.SubscribeConnections(ctx, &daemon.SubscribeConnectionsRequest{Interval: intervalMs})
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

// SubscribeStatus 推送流量/内存等运行状态。
func (c *Client) SubscribeStatus(ctx context.Context, intervalMs int64) (<-chan *daemon.Status, <-chan error) {
	ch := make(chan *daemon.Status, 8)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		stream, err := c.svc.SubscribeStatus(ctx, &daemon.SubscribeStatusRequest{Interval: intervalMs})
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
