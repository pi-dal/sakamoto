package mobilecore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/pi-dal/sakamoto/pkg/sourcesync"
)

// ValidateS3SettingsJSON checks non-secret connection settings before storage.
func ValidateS3SettingsJSON(settings string) error {
	var s sourcesync.Settings
	if json.Unmarshal([]byte(settings), &s) != nil {
		return errors.New("invalid S3 settings")
	}
	_, err := s.ObjectURL()
	return err
}

// SyncSourcesS3JSON is the shared TUI/iOS/Android wire contract. Credentials
// stay device-local; generated config JSON is never an accepted source name.
// The caller adopts bundle then saves baseline, never the reverse.
func SyncSourcesS3JSON(settings, credentials, local, baseline string) (string, error) {
	var s sourcesync.Settings
	var c sourcesync.Credentials
	var base sourcesync.Baseline
	if json.Unmarshal([]byte(settings), &s) != nil || json.Unmarshal([]byte(credentials), &c) != nil {
		return "", errors.New("invalid S3 settings or credentials")
	}
	if baseline != "" && json.Unmarshal([]byte(baseline), &base) != nil {
		return "", errors.New("invalid S3 baseline; restore local sync state")
	}
	bundle, err := sourcesync.Decode(local)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := sourcesync.Sync(ctx, s, c, bundle, base, nil)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(result)
	return string(raw), err
}

func ValidateSourceBundleJSON(raw string) error { _, err := sourcesync.Decode(raw); return err }
