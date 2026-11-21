package gen

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/pi-dal/sakamoto/pkg/regionaldns"
)

// Source URLs can carry credentials in the path as well as in the query.
func sourceRedirectPolicy(next func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		origin := via[0].URL
		if len(via) >= 10 || origin.Scheme != req.URL.Scheme || !strings.EqualFold(origin.Host, req.URL.Host) {
			return errors.New("source redirect refused; configure the final URL")
		}
		req.Header.Del("Referer")
		if next != nil {
			return next(req, via)
		}
		return nil
	}
}

// Keep both the caller's cancellation and http.Client's own request deadline,
// including while a source body is being read.
type contextTransport struct {
	ctx      context.Context
	base     http.RoundTripper
	regional regionaldns.Profile
}

func (t contextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" && (req.URL.Scheme != "http" || !net.ParseIP(req.URL.Hostname()).IsLoopback()) {
		return nil, errors.New("remote configuration sources require HTTPS")
	}
	if t.regional.ProtectsHost(req.URL.Hostname()) {
		return nil, errors.New("source fetch overlaps a protected regional DNS name")
	}
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	if t.ctx.Err() != nil {
		cancel()
	}
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	resp.Body = &contextBody{ReadCloser: resp.Body, finish: func() { stop(); cancel() }}
	return resp, nil
}

type contextBody struct {
	io.ReadCloser
	finish func()
}

func (b *contextBody) Close() error { err := b.ReadCloser.Close(); b.finish(); return err }
