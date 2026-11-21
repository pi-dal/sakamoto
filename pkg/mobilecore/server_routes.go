package mobilecore

import (
	"encoding/json"
	"fmt"
)

// EnsureProxyServerRoutesJSON retains the mobile bridge ABI. Core sockets use
// platform interface binding. A shared server IP must never exempt other apps.
func EnsureProxyServerRoutesJSON(content string) (string, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &root) != nil || root == nil {
		return "", fmt.Errorf("invalid configuration JSON")
	}
	return content, nil
}
