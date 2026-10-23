package gen

import (
	"context"
	"io"
	"net/http"
)

// Keep both the caller's cancellation and http.Client's own request deadline,
// including while a source body is being read.
type contextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t contextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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
