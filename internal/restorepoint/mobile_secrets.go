package restorepoint

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/pi-dal/sakamoto/internal/recovery"
)

// DiscardLegacyMobileSecrets runs before the first start with the new snapshot
// format. Keep retained rule assets because a later generation can reference
// them. Removing the two payloads invalidates the corresponding receipt.
func DiscardLegacyMobileSecrets(root string) error {
	base := mobileBase(root)
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	marker := filepath.Join(base, "keyless-v1")
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "profile-") {
			continue
		}
		dir := filepath.Join(base, entry.Name())
		for _, name := range []string{"config.json", "metadata.json"} {
			path := filepath.Join(dir, name)
			raw, err := recovery.ReadPrivate(path, maxInput)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if hasMobileSecret(raw, name == "metadata.json") {
				if err = os.Remove(path); err != nil {
					return err
				}
			}
		}
	}
	return recovery.Atomic(marker, []byte("1\n"), 0600)
}

func hasMobileSecret(raw []byte, metadata bool) bool {
	if metadata {
		var options struct {
			Content string `json:"configContent"`
		}
		if json.Unmarshal(raw, &options) != nil || options.Content == "" {
			return false
		}
		raw = []byte(options.Content)
	}
	var cfg struct {
		Endpoints []struct {
			Type    string
			AuthKey json.RawMessage `json:"auth_key"`
		}
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return false
	}
	for _, endpoint := range cfg.Endpoints {
		if endpoint.Type == "tailscale" && len(endpoint.AuthKey) > 0 {
			return true
		}
	}
	return false
}

func checkKeylessMobileCapture(content, metadata string) error {
	if hasMobileSecret([]byte(content), false) || hasMobileSecret([]byte(metadata), true) {
		return errors.New("remove Tailscale auth keys before capturing recovery data")
	}
	return nil
}
