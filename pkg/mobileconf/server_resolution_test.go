package mobileconf

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestProxyServerResolutionKeepsBothFamiliesAndLiteralRoutesOnFailure(t *testing.T) {
	outbounds := []map[string]any{{"server": "203.0.113.1"}, {"server": "Proxy.Example"}, {"server": "proxy.example"}, {"server": "missing.example"}}
	var mu sync.Mutex
	calls := map[string]int{}
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		mu.Lock()
		calls[host]++
		mu.Unlock()
		if host == "missing.example" {
			return nil, errors.New("unavailable")
		}
		return []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("::ffff:203.0.113.1"), netip.MustParseAddr("198.51.100.1")}, nil
	}
	routes, failed := ResolveProxyServerRoutes(context.Background(), outbounds, lookup)
	if !reflect.DeepEqual(routes, []string{"203.0.113.1/32", "198.51.100.1/32", "2001:db8::1/128"}) {
		t.Fatal(routes)
	}
	if !reflect.DeepEqual(failed, []string{"missing.example"}) || calls["proxy.example"] != 1 {
		t.Fatal(failed, calls)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	routes, failed = ResolveProxyServerRoutes(ctx, outbounds, func(ctx context.Context, _ string) ([]netip.Addr, error) { <-ctx.Done(); return nil, ctx.Err() })
	if time.Since(start) > time.Second || len(failed) != 2 || !reflect.DeepEqual(routes, []string{"203.0.113.1/32"}) {
		t.Fatal("resolution was not bounded or lost literal routes", routes, failed)
	}
}
