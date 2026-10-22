package mobilelocation

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
)

var hosts = []string{"gs-loc.apple.com", "gs-loc-cn.apple.com", "gsp-ssl.ls.apple.com", "bluedot.is.autonavi.com", "bluedot.is.autonavi.com.gds.alibabadns.com"}

func locationHost(raw string) bool {
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.TrimSuffix(strings.ToLower(raw), ".")
	for _, host := range hosts {
		if raw == host {
			return true
		}
	}
	return false
}

// HostsJSON is shared with NEProxySettings; no broad wildcard interception.
func HostsJSON() string { b, _ := json.Marshal(hosts); return string(b) }

func decodeSettings(raw string) (Settings, error) {
	var s Settings
	if len(raw) > 4096 {
		return s, errors.New("location settings too large")
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, errors.New("invalid location settings")
	}
	return s, s.Validate()
}

type Authority struct {
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"privateKey"`
	Fingerprint string `json:"fingerprint"`
}

func GenerateAuthorityJSON() (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", err
	}
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Sakamoto Location CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		return "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	a := Authority{string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})), hex.EncodeToString(sum[:])}
	encoded, err := json.Marshal(a)
	return string(encoded), err
}

func parseAuthority(raw string) (tls.Certificate, error) {
	var a Authority
	if len(raw) > 32768 || json.Unmarshal([]byte(raw), &a) != nil {
		return tls.Certificate{}, errors.New("invalid location authority")
	}
	cert, err := tls.X509KeyPair([]byte(a.Certificate), []byte(a.PrivateKey))
	if err != nil {
		return cert, errors.New("location authority key mismatch")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || !leaf.IsCA || !leaf.BasicConstraintsValid || leaf.KeyUsage&x509.KeyUsageCertSign == 0 || time.Now().After(leaf.NotAfter) {
		return cert, errors.New("location authority is not a valid CA")
	}
	cert.Leaf = leaf
	return cert, nil
}

// ValidateAuthorityJSON never returns private material in an error.
func ValidateAuthorityJSON(raw string) error { _, err := parseAuthority(raw); return err }

type Snapshot struct {
	Enabled     bool   `json:"enabled"`
	Connections int64  `json:"connections"`
	Requests    int64  `json:"requests"`
	Patched     int64  `json:"patched"`
	Skipped     int64  `json:"skipped"`
	TLSFailures int64  `json:"tlsFailures"`
	LastRequest int64  `json:"lastRequest"`
	LastPatch   int64  `json:"lastPatch"`
	Error       string `json:"error"`
}

type Proxy struct {
	mu        sync.Mutex
	settings  Settings
	snapshot  Snapshot
	server    *http.Server
	listener  net.Listener
	transport *http.Transport
	handler   *goproxy.ProxyHttpServer
	slots     chan struct{}
	closed    bool
	fallback  string
	clients   map[string]*proxyConnection
}

// NewProxy's callback remains Go-side; gomobile uses the libbox factory.
func NewProxy(authorityJSON, settingsJSON string, dial func(context.Context, string, string) (net.Conn, error)) (*Proxy, error) {
	cert, err := parseAuthority(authorityJSON)
	if err != nil {
		return nil, err
	}
	settings, err := decodeSettings(settingsJSON)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &Proxy{settings: settings, listener: listener, slots: make(chan struct{}, 4), clients: make(map[string]*proxyConnection)}
	p.transport = &http.Transport{Proxy: func(req *http.Request) (*url.URL, error) {
		p.mu.Lock()
		fallback := p.fallback
		p.mu.Unlock()
		if !locationHost(req.URL.Host) && fallback != "" {
			return url.Parse(fallback)
		}
		return nil, nil
	}, MaxResponseHeaderBytes: 32768, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		p.mu.Lock()
		fallback := strings.TrimPrefix(p.fallback, "http://")
		p.mu.Unlock()
		if fallback != "" && address == fallback {
			return (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, address)
		}
		return dial(ctx, network, address)
	}, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 8 * time.Second, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 15 * time.Second, ForceAttemptHTTP2: false}
	handler := goproxy.NewProxyHttpServer()
	p.handler = handler
	handler.Tr = p.transport
	handler.CertStore = &certificateCache{values: make(map[string]*tls.Certificate)}
	handler.Logger = log.New(proxyLog{p}, "", 0)
	handler.ConnectDialWithReq = func(req *http.Request, network, address string) (net.Conn, error) {
		p.mu.Lock()
		fallback := p.fallback
		p.mu.Unlock()
		if !locationHost(address) && fallback != "" {
			return handler.NewConnectDialToProxy(fallback)(network, address)
		}
		return dial(req.Context(), network, address)
	}
	certificateTLS := goproxy.TLSConfigFromCA(&cert)
	action := &goproxy.ConnectAction{Action: goproxy.ConnectMitm, TLSConfig: func(host string, ctx *goproxy.ProxyCtx) (*tls.Config, error) {
		configuration, err := certificateTLS(host, ctx)
		if err == nil {
			configuration.MinVersion = tls.VersionTLS12
			configuration.NextProtos = []string{"http/1.1"}
		}
		return configuration, err
	}}
	handler.OnRequest().HandleConnectFunc(func(address string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		if locationHost(address) {
			p.mu.Lock()
			enabled := p.settings.Enabled
			conn := p.clients[ctx.Req.RemoteAddr]
			if enabled && conn != nil {
				select {
				case p.slots <- struct{}{}:
					conn.location = true
				default:
					p.mu.Unlock()
					return goproxy.RejectConnect, address
				}
				p.snapshot.Connections++
				p.mu.Unlock()
				_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
				return action, address
			}
			p.mu.Unlock()
		}
		return goproxy.OkConnect, address
	})
	handler.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		if locationHost(req.Host) {
			requestContext, cancel := context.WithTimeout(req.Context(), 12*time.Second)
			ctx.UserData = cancel
			req = req.WithContext(requestContext)
		}
		if locationRequest(req) {
			p.mu.Lock()
			p.snapshot.Requests++
			p.snapshot.LastRequest = time.Now().Unix()
			p.mu.Unlock()
		}
		return req, nil
	})
	handler.OnResponse().DoFunc(p.process)
	handler.NonproxyHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Proxy requests only", http.StatusBadRequest)
	})
	p.server = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16384}
	go func() { _ = p.server.Serve(proxyListener{Listener: listener, proxy: p}) }()
	return p, nil
}

type certificateCache struct {
	mu     sync.Mutex
	values map[string]*tls.Certificate
}

func (c *certificateCache) Fetch(host string, generate func() (*tls.Certificate, error)) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if value := c.values[host]; value != nil {
		return value, nil
	}
	value, err := generate()
	if err == nil && len(c.values) < len(hosts) {
		c.values[host] = value
	}
	return value, err
}

type proxyLog struct{ proxy *Proxy }

func (w proxyLog) Write(b []byte) (int, error) {
	text := strings.ToLower(string(b))
	p := w.proxy
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.Contains(text, "handshake") || strings.Contains(text, "tls") {
		p.snapshot.TLSFailures++
		p.snapshot.Error = "TLS processing failed; check CA trust, upstream reachability and iOS compatibility"
	}
	return len(b), nil
}

func (p *Proxy) Port() int { return p.listener.Addr().(*net.TCPAddr).Port }
func (p *Proxy) Apply(settingsJSON string) error {
	settings, err := decodeSettings(settingsJSON)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("location proxy closed")
	}
	p.settings = settings
	return nil
}

// Preserve the original explicit HTTP proxy for unrelated traffic. Location
// traffic uses the core route callback, not an unprotected direct socket.
func (p *Proxy) SetFallback(address string) error {
	if address != "" {
		host, port, err := net.SplitHostPort(address)
		n, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || n < 1 || n > 65535 || host != "127.0.0.1" && host != "::1" {
			return errors.New("fallback must be a loopback core proxy")
		}
		if n == p.Port() {
			return errors.New("location proxy cannot forward to itself")
		}
		address = "http://" + address
	}
	p.mu.Lock()
	p.fallback = address
	p.mu.Unlock()
	p.transport.CloseIdleConnections()
	return nil
}
func (p *Proxy) SnapshotJSON() string {
	p.mu.Lock()
	s := p.snapshot
	s.Enabled = p.settings.Enabled && !p.closed
	p.mu.Unlock()
	b, _ := json.Marshal(s)
	return string(b)
}
func (p *Proxy) Close() error {
	p.mu.Lock()
	p.closed = true
	p.settings.Enabled = false
	clients := make([]*proxyConnection, 0, len(p.clients))
	for _, c := range p.clients {
		clients = append(clients, c)
	}
	p.mu.Unlock()
	p.transport.CloseIdleConnections()
	err := p.server.Close()
	for _, c := range clients {
		_ = c.Close()
	}
	return err
}

type proxyListener struct {
	net.Listener
	proxy *Proxy
}

func (l proxyListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		p := l.proxy
		p.mu.Lock()
		if p.closed || len(p.clients) >= 64 {
			p.mu.Unlock()
			_ = c.Close()
			continue
		}
		wrapped := &proxyConnection{Conn: c, proxy: p}
		p.clients[c.RemoteAddr().String()] = wrapped
		p.mu.Unlock()
		return wrapped, nil
	}
}

type proxyConnection struct {
	net.Conn
	proxy    *Proxy
	once     sync.Once
	location bool
	readMu   sync.Mutex
	received int64
}

func (c *proxyConnection) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	c.proxy.mu.Lock()
	limited := c.location
	c.proxy.mu.Unlock()
	if !limited {
		return c.Conn.Read(b)
	}
	// ReadRequest inside a hijacked CONNECT is outside net/http's header
	// limit. Cap the selected TLS stream, including its inner headers.
	remaining := int64(2*bodyLimit) - c.received
	if remaining <= 0 {
		_ = c.Close()
		return 0, errors.New("location request budget exceeded")
	}
	if int64(len(b)) > remaining {
		b = b[:remaining]
	}
	n, err := c.Conn.Read(b)
	c.received += int64(n)
	return n, err
}
func (c *proxyConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		p := c.proxy
		p.mu.Lock()
		delete(p.clients, c.RemoteAddr().String())
		location := c.location
		p.mu.Unlock()
		if location {
			<-p.slots
		}
	})
	return err
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error { err := b.ReadCloser.Close(); b.cancel(); return err }
func locationRequest(req *http.Request) bool {
	return req != nil && locationHost(req.Host) && req.URL != nil && req.URL.Path == "/clls/wloc" && req.Method == http.MethodPost
}
func (p *Proxy) process(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
	if cancel, ok := ctx.UserData.(context.CancelFunc); ok {
		if resp == nil || resp.Body == nil {
			cancel()
		} else {
			resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
		}
	}
	if resp == nil {
		if locationRequest(ctx.Req) {
			p.mu.Lock()
			p.snapshot.Error = "Location upstream failed; check route and connectivity"
			p.mu.Unlock()
		}
		return resp
	}
	if !locationRequest(resp.Request) {
		return resp
	}
	p.mu.Lock()
	settings := p.settings
	p.mu.Unlock()
	if !settings.Enabled || resp.StatusCode != http.StatusOK || resp.Body == nil {
		return resp
	}
	if resp.ContentLength > bodyLimit {
		p.skip("Location response exceeds processing budget")
		return resp
	}
	original := resp.Body
	body, err := io.ReadAll(io.LimitReader(original, bodyLimit+1))
	if err != nil {
		_ = original.Close()
		p.skip("Location upstream body did not complete")
		resp.StatusCode = http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			resp.StatusCode = http.StatusGatewayTimeout
		}
		resp.Status = strconv.Itoa(resp.StatusCode) + " " + http.StatusText(resp.StatusCode)
		message := "Location upstream body did not complete\n"
		resp.Body = io.NopCloser(strings.NewReader(message))
		resp.ContentLength = int64(len(message))
		resp.TransferEncoding = nil
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Transfer-Encoding")
		resp.Header.Set("Content-Type", "text/plain")
		resp.Header.Set("Content-Length", strconv.Itoa(len(message)))
		return resp
	}
	if len(body) > bodyLimit {
		resp.Body = &replayBody{Reader: io.MultiReader(bytes.NewReader(body), original), closer: original}
		p.skip("Location response exceeds budget or could not be read")
		return resp
	}
	_ = original.Close()
	// Route ticks can change the target while the upstream body is arriving.
	// Choose the latest point after reading it; disable must still win.
	p.mu.Lock()
	settings = p.settings
	closed := p.closed
	p.mu.Unlock()
	if !settings.Enabled || closed {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp
	}
	replacement, count, err := patchBody(body, settings)
	if err != nil || count == 0 {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		p.skip("Unrecognized location response; passed through unchanged")
		return resp
	}
	// Clearing/updating coordinates while a response is in flight must not
	// publish an obsolete setting after the control operation has completed.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.settings != settings || p.closed {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp
	}
	resp.Body = io.NopCloser(bytes.NewReader(replacement))
	resp.ContentLength = int64(len(replacement))
	resp.TransferEncoding = nil
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("ETag")
	resp.Header.Del("Content-MD5")
	resp.Header.Del("Expires")
	resp.Header.Set("Cache-Control", "no-store")
	resp.Header.Del("Transfer-Encoding")
	resp.Header.Set("Content-Length", strconv.Itoa(len(replacement)))
	p.snapshot.Patched++
	p.snapshot.LastPatch = time.Now().Unix()
	p.snapshot.Error = ""
	return resp
}
func (p *Proxy) skip(message string) {
	p.mu.Lock()
	p.snapshot.Skipped++
	p.snapshot.Error = message
	p.mu.Unlock()
}

type replayBody struct {
	io.Reader
	closer io.Closer
}

func (b *replayBody) Close() error { return b.closer.Close() }

// CertificateServer runs in the foreground app for Safari installation and
// a default-system-trust TLS check. It never exposes the authority private key.
type CertificateServer struct {
	httpServer, httpsServer     *http.Server
	httpListener, httpsListener net.Listener
	token                       string
	profile                     string
}

func NewCertificateServer(authorityJSON string) (*CertificateServer, error) {
	ca, err := parseAuthority(authorityJSON)
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Sakamoto CA trust check"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.Leaf, &key.PublicKey, ca.PrivateKey)
	if err != nil {
		return nil, err
	}
	leaf := tls.Certificate{Certificate: [][]byte{der, ca.Certificate[0]}, PrivateKey: key}
	h, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	t, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = h.Close()
		return nil, err
	}
	tokenBytes := make([]byte, 24)
	if _, err = rand.Read(tokenBytes); err != nil {
		_ = h.Close()
		_ = t.Close()
		return nil, err
	}
	s := &CertificateServer{httpListener: h, httpsListener: t, token: hex.EncodeToString(tokenBytes)}
	sum := sha256.Sum256(ca.Certificate[0])
	identity := hex.EncodeToString(sum[:16])
	uuid := func(b []byte) string {
		h := hex.EncodeToString(b)
		return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
	}
	certificateID, profileID := uuid(sum[:16]), uuid(sum[16:])
	profile := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>PayloadContent</key><array><dict><key>PayloadType</key><string>com.apple.security.root</string><key>PayloadVersion</key><integer>1</integer><key>PayloadIdentifier</key><string>com.pidal.sakamoto.location.ca.%s</string><key>PayloadUUID</key><string>%s</string><key>PayloadDisplayName</key><string>Sakamoto Location CA</string><key>PayloadContent</key><data>%s</data></dict></array><key>PayloadType</key><string>Configuration</string><key>PayloadVersion</key><integer>1</integer><key>PayloadIdentifier</key><string>com.pidal.sakamoto.location.%s</string><key>PayloadUUID</key><string>%s</string><key>PayloadDisplayName</key><string>Sakamoto Location Certificate</string><key>PayloadDescription</key><string>Device-specific certificate for optional network-location simulation.</string></dict></plist>`, identity, certificateID, base64.StdEncoding.EncodeToString(ca.Certificate[0]), identity, profileID)
	s.profile = profile
	s.httpServer = &http.Server{ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/location.mobileconfig" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-apple-aspen-config")
		w.Header().Set("Content-Disposition", "attachment; filename=sakamoto-location.mobileconfig")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, profile)
	})}
	s.httpsServer = &http.Server{ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 5 * time.Second, ErrorLog: log.New(io.Discard, "", 0), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trust" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, s.token)
	})}
	go func() { _ = s.httpServer.Serve(h) }()
	go func() {
		_ = s.httpsServer.Serve(tls.NewListener(t, &tls.Config{Certificates: []tls.Certificate{leaf}, MinVersion: tls.VersionTLS12}))
	}()
	return s, nil
}
func (s *CertificateServer) Profile() string { return s.profile }
func (s *CertificateServer) DownloadURL() string {
	return "http://" + s.httpListener.Addr().String() + "/location.mobileconfig"
}
func (s *CertificateServer) ProbeURL() string {
	return "https://" + s.httpsListener.Addr().String() + "/trust"
}
func (s *CertificateServer) Token() string { return s.token }
func (s *CertificateServer) Close() error {
	a := s.httpServer.Close()
	b := s.httpsServer.Close()
	return errors.Join(a, b)
}
