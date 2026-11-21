package mobileconf

import (
	"errors"
	"strings"
)

// ConfigureVMess maps the session transport shared by the link and Clash importers.
func ConfigureVMess(node map[string]any, security, serverName, network, host, path, header string) error {
	switch strings.ToLower(security) {
	case "tls", "true", "1":
		tls := map[string]any{"enabled": true}
		if serverName != "" {
			tls["server_name"] = serverName
		}
		node["tls"] = tls
	case "", "none", "false", "0":
	default:
		return errors.New("unsupported VMess TLS mode")
	}
	if header != "" && header != "none" {
		return errors.New("unsupported VMess transport header")
	}
	switch strings.ToLower(network) {
	case "", "tcp", "none":
	case "ws", "websocket":
		ws := map[string]any{"type": "ws", "path": path}
		if host != "" {
			ws["headers"] = map[string]string{"Host": host}
		}
		node["transport"] = ws
	default:
		return errors.New("unsupported VMess transport")
	}
	return nil
}
