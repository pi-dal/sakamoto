package mobileconf

import "net/netip"

// ProxyServerRoutes excludes literal proxy endpoints from TUN capture. Hostnames
// are left to outbound DNS/interface binding; resolving them here would freeze
// a possibly rotating address and make generation depend on local DNS.
func ProxyServerRoutes(outbounds []map[string]any) []string {
	var routes []string
	seen := map[netip.Addr]bool{}
	for _, outbound := range outbounds {
		server, _ := outbound["server"].(string)
		address, err := netip.ParseAddr(server)
		if err != nil || address.Zone() != "" {
			continue
		}
		address = address.Unmap()
		if seen[address] {
			continue
		}
		seen[address] = true
		routes = append(routes, netip.PrefixFrom(address, address.BitLen()).String())
	}
	return routes
}
