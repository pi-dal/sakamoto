package config

// MobileDNSGuard carries the resolver policy across provider regeneration.
// The desktop network service name is local to the system DNS transaction.
type MobileDNSGuard struct {
	LocalDomains        []string `json:"local_domains"`
	BootstrapIP         string   `json:"bootstrap_ip"`
	BootstrapServerName string   `json:"bootstrap_server_name"`
}
