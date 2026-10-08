package mobilegen

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Mobile generation retries a transient read-only fetch once. Permanent HTTP
// failures retain their status; no rules or subscriptions are silently omitted.
// The client's timeout bounds the entire request, including its retry.
type sourceTransport struct{ base http.RoundTripper }

func (t sourceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req := request.Clone(request.Context())
		req.Header = request.Header.Clone()
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", "sakamoto/0.1 (source generation)")
		}
		resp, err := t.base.RoundTrip(req)
		retry := false
		if err != nil {
			var ne interface{ Timeout() bool }
			retry = errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
			if failure, ok := err.(nativeFailure); ok {
				retry = failure == "timeout" || failure == "dns" || failure == "network"
			}
		} else {
			switch resp.StatusCode {
			case 429, 502, 503, 504:
				retry = true
			}
		}
		if !retry || attempt == 1 || request.Method != http.MethodGet || request.Context().Err() != nil {
			return resp, err
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-timer.C:
		case <-request.Context().Done():
			timer.Stop()
			return nil, request.Context().Err()
		}
	}
}

func sourceClient(fetcher SourceFetcher) *http.Client {
	transport := http.DefaultTransport
	if fetcher != nil {
		transport = &nativeTransport{fetcher: fetcher}
	}
	return &http.Client{Timeout: 65 * time.Second, Transport: sourceTransport{base: transport}}
}

// SourceFetcher lets iOS use URLSession, including system network/proxy
// settings, instead of a second networking stack for generation.
type SourceFetcher interface {
	Fetch(source string) (*SourceResponse, error)
}
type SourceResponse struct {
	StatusCode int32
	Body       []byte
	Failure    string
}

type nativeTransport struct {
	fetcher SourceFetcher
	mu      sync.Mutex
}

func (t *nativeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.Lock()
	response, err := t.fetcher.Fetch(request.URL.String())
	t.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("native source request failed")
	}
	if response == nil {
		return nil, fmt.Errorf("native source response missing")
	}
	if response.Failure != "" {
		switch response.Failure {
		case "timeout", "dns", "offline", "tls", "network", "too-large":
			return nil, nativeFailure(response.Failure)
		default:
			return nil, fmt.Errorf("native source request failed")
		}
	}
	if len(response.Body) > 16<<20 {
		return nil, nativeFailure("too-large")
	}
	if response.StatusCode == http.StatusOK {
		head := strings.ToLower(strings.TrimSpace(string(response.Body[:min(len(response.Body), 512)])))
		if strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html") {
			return nil, fmt.Errorf("source returned an HTML page instead of configuration data")
		}
	}
	return &http.Response{StatusCode: int(response.StatusCode), Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(response.Body)), Request: request}, nil
}

type nativeFailure string

func (e nativeFailure) Error() string {
	switch e {
	case "timeout":
		return "request timed out"
	case "dns":
		return "DNS lookup failed"
	case "offline":
		return "network is offline"
	case "tls":
		return "TLS certificate or connection failed"
	case "too-large":
		return "source exceeds the 16 MiB limit"
	default:
		return "network connection failed"
	}
}
func (e nativeFailure) Timeout() bool { return e == "timeout" }
