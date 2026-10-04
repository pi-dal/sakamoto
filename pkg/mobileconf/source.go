package mobileconf

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidSource accepts a local .conf path or an HTTP(S) URL, exactly like the
// host importer (internal/gen delegates here). Other schemes are rejected.
// On iOS a "local path" only makes sense as a Files App document; the Swift
// layer decides which entry points to offer, this only validates the shape.
func ValidSource(source string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return fmt.Errorf("provide a Shadowrocket .conf URL or local path")
	}
	u, err := url.Parse(source)
	if err != nil {
		return err
	}
	if strings.Contains(source, "://") && u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only HTTP(S) URLs or local paths are supported")
	}
	if (u.Scheme == "http" || u.Scheme == "https") && u.Host == "" {
		return fmt.Errorf("URL is missing a host")
	}
	return nil
}

// IsRemote reports whether the source is an HTTP(S) URL (as opposed to a
// local path).
func IsRemote(source string) bool {
	u, err := url.Parse(source)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https")
}

// DisplaySource masks the credential-bearing parts of a source for safe
// display and error text: userinfo, query, fragment are dropped and the path
// is shortened to /… for remote URLs. Local paths pass through unchanged.
func DisplaySource(source string) string {
	if !IsRemote(source) {
		return source
	}
	u, _ := url.Parse(source)
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	if u.Path != "" && u.Path != "/" {
		u.Path = "/…"
		u.RawPath = ""
	}
	return u.String()
}
