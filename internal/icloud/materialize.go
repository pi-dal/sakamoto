package icloud

import (
	"errors"
	"fmt"
	"syscall"
)

// Unavailable files are never treated as absent or replaced with local bytes.
// They defer their source family while unrelated sources can still sync.
var errSourceDownloading = errors.New("iCloud source is awaiting download")

func downloadingSource(path string, cause error) error {
	requestSourceDownload(path)
	return fmt.Errorf("%w: %w", errSourceDownloading, cause)
}

func sourceUnavailable(err error) bool {
	return errors.Is(err, errSourceDownloading) || errors.Is(err, syscall.EDEADLK)
}
