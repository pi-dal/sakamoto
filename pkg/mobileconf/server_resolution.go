package mobileconf

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"sync"
)

// ResolveProxyServerRoutes takes a bounded generation-time DNS snapshot. It
// resolves each hostname once, keeps both address families, and never emits a
// hostname as a CIDR. Failed lookups retain literal routes and are reported.
func ResolveProxyServerRoutes(ctx context.Context, outbounds []map[string]any, lookup func(context.Context, string) ([]netip.Addr, error)) ([]string, []string) {
	routes := ProxyServerRoutes(outbounds)
	seen := map[string]bool{}
	var hosts []string
	for _, outbound := range outbounds {
		server, _ := outbound["server"].(string)
		if server == "" {
			continue
		}
		if _, err := netip.ParseAddr(server); err == nil {
			continue
		}
		host := strings.ToLower(server)
		if !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	addresses := make([][]netip.Addr, len(hosts))
	failed := make([]bool, len(hosts))
	var workers sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	for index, host := range hosts {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				failed[index] = true
				return
			}
			defer func() { <-semaphore }()
			var err error
			addresses[index], err = lookup(ctx, host)
			failed[index] = err != nil || len(addresses[index]) == 0
		}()
	}
	workers.Wait()
	routeSeen := map[string]bool{}
	for _, route := range routes {
		routeSeen[route] = true
	}
	var unresolved []string
	for index, host := range hosts {
		if failed[index] {
			unresolved = append(unresolved, host)
			continue
		}
		slices.SortFunc(addresses[index], func(a, b netip.Addr) int { return a.Compare(b) })
		for _, address := range addresses[index] {
			if !address.IsValid() || address.Zone() != "" {
				continue
			}
			address = address.Unmap()
			route := netip.PrefixFrom(address, address.BitLen()).String()
			if !routeSeen[route] {
				routeSeen[route] = true
				routes = append(routes, route)
			}
		}
	}
	return routes, unresolved
}
