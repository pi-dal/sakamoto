#!/usr/bin/env python3
"""Add the bounded location proxy factory to the private iOS libbox copy."""
from pathlib import Path
import sys

root = Path(sys.argv[1])
(root / "experimental/libbox/sakamoto_location.go").write_text('''package libbox

import (
    "context"
    "net"
    "github.com/pi-dal/sakamoto/pkg/mobilelocation"
)

// Factory shares the existing Go runtime. Each upstream stream follows the
// current core router and instance lifecycle; no default direct dial occurs.
func (s *CommandServer) NewLocationProxy(authorityJSON, settingsJSON string) (*mobilelocation.Proxy, error) {
    return mobilelocation.NewProxy(authorityJSON, settingsJSON,
        func(ctx context.Context, network, address string) (net.Conn, error) {
            return s.StartedService.OpenLocationStream(ctx, network, address)
        })
}
''')
(root / "daemon/sakamoto_location.go").write_text('''package daemon

import (
    "context"
    "fmt"
    "net"
    "github.com/sagernet/sing-box/adapter"
    M "github.com/sagernet/sing/common/metadata"
)

type locationStream struct { net.Conn; cancel context.CancelFunc }
func (c *locationStream) Close() error { c.cancel(); return c.Conn.Close() }

func (s *StartedService) OpenLocationStream(requestCtx context.Context, network, address string) (net.Conn, error) {
    if err := requestCtx.Err(); err != nil { return nil, err }
    if network != "tcp" { return nil, fmt.Errorf("location proxy requires TCP") }
    s.serviceAccess.RLock()
    instance := s.instance
    s.serviceAccess.RUnlock()
    if instance == nil || instance.ctx.Err() != nil { return nil, fmt.Errorf("VPN core is unavailable") }
    client, routed := net.Pipe()
    ctx, cancel := context.WithCancel(instance.ctx)
    context.AfterFunc(ctx, func() { client.Close(); routed.Close() })
    // The transport owns cancellation after the dial returns. A HTTP dial
    // context may be cancelled immediately on return, so it must not become
    // the lifetime of an established/pooled stream.
    go instance.Box().Router().RouteConnectionEx(ctx, routed, adapter.InboundContext{
        Inbound: "sakamoto-location", InboundType: "mixed", Network: "tcp",
        Source: M.ParseSocksaddr("127.0.0.1:0"), Destination: M.ParseSocksaddr(address),
    }, func(error) { cancel() })
    return &locationStream{Conn: client, cancel: cancel}, nil
}
''')
