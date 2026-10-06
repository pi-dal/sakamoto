package sourcesync

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Settings never serializes credentials into the synced bundle.
type Settings struct {
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	Endpoint string `json:"endpoint" yaml:"endpoint"`
	Region   string `json:"region" yaml:"region"`
	Bucket   string `json:"bucket" yaml:"bucket"`
	Prefix   string `json:"prefix" yaml:"prefix"`
}
type Credentials struct {
	AccessKey    string `json:"access_key"`
	SecretKey    string `json:"secret_key"`
	SessionToken string `json:"session_token,omitempty"`
}
type Result struct {
	Bundle    Bundle   `json:"bundle"`
	Baseline  Baseline `json:"baseline"`
	Downloads []string `json:"downloads"`
	Uploaded  bool     `json:"uploaded"`
}

func (s Settings) ObjectURL() (*url.URL, error) {
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("S3 endpoint must be an HTTPS URL without credentials, query or fragment")
	}
	if s.Region == "" || strings.ContainsAny(s.Region, "/ \t\r\n") {
		return nil, errors.New("set the S3 region (use auto for R2)")
	}
	if s.Bucket == "" || strings.ContainsAny(s.Bucket, "/\\ \t\r\n?#") || s.Bucket == "." || s.Bucket == ".." {
		return nil, errors.New("set a valid S3 bucket")
	}
	prefix := strings.Trim(s.Prefix, "/")
	if strings.ContainsAny(prefix, "\\\x00\r\n") {
		return nil, errors.New("invalid S3 prefix")
	}
	for _, part := range strings.Split(prefix, "/") {
		if part == "." || part == ".." {
			return nil, errors.New("invalid S3 prefix")
		}
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + s.Bucket + "/"
	if prefix != "" {
		u.Path += prefix + "/"
	}
	u.Path += "sources-v1.json"
	u.RawPath = ""
	return u, nil
}
func mac(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(value))
	return h.Sum(nil)
}
func awsPath(value string) string {
	const digits = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		b := value[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || strings.ContainsRune("-_.~/", rune(b)) {
			out.WriteByte(b)
		} else {
			out.WriteByte('%')
			out.WriteByte(digits[b>>4])
			out.WriteByte(digits[b&15])
		}
	}
	return out.String()
}
func sign(req *http.Request, body []byte, region string, c Credentials, now time.Time) {
	timestamp := now.UTC().Format("20060102T150405Z")
	date := timestamp[:8]
	req.Header.Set("X-Amz-Date", timestamp)
	sum := sha256.Sum256(body)
	payload := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Content-Sha256", payload)
	values := map[string]string{"host": req.URL.Host, "x-amz-content-sha256": payload, "x-amz-date": timestamp}
	if c.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", c.SessionToken)
		values["x-amz-security-token"] = strings.TrimSpace(c.SessionToken)
	}
	for _, key := range []string{"If-Match", "If-None-Match"} {
		if value := req.Header.Get(key); value != "" {
			values[strings.ToLower(key)] = value
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var headerBlock strings.Builder
	for _, name := range names {
		headerBlock.WriteString(name + ":" + values[name] + "\n")
	}
	headers, signed := headerBlock.String(), strings.Join(names, ";")
	// url.PathEscape escapes RFC3986 query delimiters; preserve slash separators.
	uri := awsPath(req.URL.Path)
	req.URL.RawPath = uri
	canonical := req.Method + "\n" + uri + "\n\n" + headers + "\n" + signed + "\n" + payload
	hashed := sha256.Sum256([]byte(canonical))
	scope := date + "/" + region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + timestamp + "\n" + scope + "\n" + hex.EncodeToString(hashed[:])
	key := mac([]byte("AWS4"+c.SecretKey), date)
	key = mac(key, region)
	key = mac(key, "s3")
	key = mac(key, "aws4_request")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+hex.EncodeToString(mac(key, toSign)))
}
func request(ctx context.Context, client *http.Client, method string, u *url.URL, body []byte, c Credentials, region, etag string) ([]byte, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, "", false, errors.New("cannot prepare S3 request")
	}
	if method == "PUT" {
		req.Header.Set("Content-Type", "application/json")
		if etag == "" {
			req.Header.Set("If-None-Match", "*")
		} else {
			req.Header.Set("If-Match", etag)
		}
	}
	sign(req, body, region, c, time.Now())
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", false, errors.New("S3 request failed; check endpoint and network")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 404 && method == "GET" {
		return nil, "", false, nil
	}
	if resp.StatusCode == 409 || resp.StatusCode == 412 {
		return nil, "", false, errors.New("sync conflict: remote bundle changed during sync; retry")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", false, fmt.Errorf("S3 returned HTTP %d; check bucket permissions and region", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil || len(b) > MaxBytes {
		return nil, "", false, errors.New("S3 response unreadable or exceeds 32 MiB")
	}
	return b, resp.Header.Get("ETag"), true, nil
}

// Sync returns an adoption plan. Persist the returned baseline ONLY after the
// merged sources have been adopted locally. Conditional writes prevent lost
// remote edits; redirects are disabled so signed credentials never follow them.
func Sync(ctx context.Context, s Settings, c Credentials, local Bundle, base Baseline, client *http.Client) (Result, error) {
	if !s.Enabled {
		return Result{}, errors.New("S3 sync is disabled")
	}
	u, err := s.ObjectURL()
	if err != nil {
		return Result{}, err
	}
	if c.AccessKey == "" || c.SecretKey == "" || strings.ContainsAny(c.AccessKey+c.SessionToken, "\r\n") || strings.ContainsAny(c.AccessKey, " ,/\t") {
		return Result{}, errors.New("set S3 access key and secret key")
	}
	if err := local.Validate(); err != nil {
		return Result{}, err
	}
	local = Canonical(local)
	target := u.String()
	if base.Target != "" && base.Target != target {
		base = Baseline{}
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	safe := *client
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	raw, etag, exists, err := request(ctx, &safe, "GET", u, nil, c, s.Region, "")
	if err != nil {
		return Result{}, err
	}
	remote := Bundle{Version: 1, Files: map[string]string{}}
	if exists {
		if etag == "" {
			return Result{}, errors.New("S3 object has no ETag; safe sync requires conditional writes")
		}
		remote, err = Decode(string(raw))
		if err != nil {
			return Result{}, err
		}
	}
	merged, downloads, err := Merge(local, remote, base)
	if err != nil {
		return Result{}, err
	}
	result := Result{Bundle: merged, Baseline: baseline(merged, target), Downloads: downloads}
	next, _ := json.Marshal(merged)
	if len(next) > MaxBytes {
		return Result{}, errors.New("encoded S3 source bundle exceeds 32 MiB")
	}
	previous, _ := json.Marshal(remote)
	if !bytes.Equal(next, previous) {
		if _, _, _, err = request(ctx, &safe, "PUT", u, next, c, s.Region, etag); err != nil {
			return Result{}, err
		}
		result.Uploaded = true
	}
	return result, nil
}
