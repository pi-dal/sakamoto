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
	request := NewS3SyncRequest()
	return request.Run(settings, credentials, local, baseline)
}

// S3SyncRequest binds the caller's lifetime to both signed HTTP requests.
type S3SyncRequest struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func NewS3SyncRequest() *S3SyncRequest {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	return &S3SyncRequest{ctx: ctx, cancel: cancel}
}

func (r *S3SyncRequest) Cancel() { r.cancel() }

func (r *S3SyncRequest) Run(settings, credentials, local, baseline string) (string, error) {
	defer r.Cancel()
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
	result, err := sourcesync.Sync(r.ctx, s, c, bundle, base, nil)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(result)
	return string(raw), err
}

func ValidateSourceBundleJSON(raw string) error { _, err := sourcesync.Decode(raw); return err }
