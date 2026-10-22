#!/usr/bin/env python3
"""Apply checked recovery fixes to a private libbox v1.14.2 copy."""
from pathlib import Path
import sys


def replace_once(text, old, new, label):
    if new in text:
        return text
    if text.count(old) != 1:
        raise RuntimeError(f"Upstream {label} drift: expected one anchor")
    return text.replace(old, new)


def patch(root):
    server = root / "experimental/libbox/command_server.go"
    source = replace_once(server.read_text(), '''\tif !C.IsIos {
\t\tinstance.PauseManager().DeviceWake()
\t}
''', '''\t// Resume immediately on an NE wake; the minute timer is a fallback.
\tif s.endPauseTimer != nil {
\t\ts.endPauseTimer.Stop()
\t}
\tinstance.PauseManager().DeviceWake()
''', "device wake")
    monitor = root / "experimental/libbox/monitor.go"
    network = replace_once(monitor.read_text(), 'import (\n', 'import (\n\t"slices"\n', "monitor imports")
    network = replace_once(network,
        'oldInterface.Name == m.defaultInterface.Name && oldInterface.Index == m.defaultInterface.Index {',
        '''oldInterface.Name == m.defaultInterface.Name && oldInterface.Index == m.defaultInterface.Index &&
\t\toldInterface.MTU == m.defaultInterface.MTU && oldInterface.Flags == m.defaultInterface.Flags &&
\t\tslices.Equal(oldInterface.Addresses, m.defaultInterface.Addresses) {''', "interface address update")
    endpoint = root / "protocol/tailscale/endpoint.go"
    transport = replace_once(endpoint.read_text(), '\tfallbackTCPCloser   func()\n',
        '\tfallbackTCPCloser   func()\n\ttransportConnections adapter.ConnectionManager\n', "transport owner")
    transport = replace_once(transport, '''\toutboundDialer, err := dialer.NewWithOptions(dialer.Options{
\t\tContext:          ctx,
''', '''\ttransportContext, transportConnections := mobileTransportContext(ctx, logger)
\toutboundDialer, err := dialer.NewWithOptions(dialer.Options{
\t\tContext:          transportContext,
''', "transport dialer")
    transport = replace_once(transport, '\t\tkeyAuth:                    options.AuthKey != "",\n',
        '\t\tkeyAuth:                    options.AuthKey != "",\n\t\ttransportConnections:       transportConnections,\n', "transport construction")
    transport = replace_once(transport, 'func (t *Endpoint) Close() error {\n', '''func (t *Endpoint) Close() error {
\t// Tailscale owns these sockets across interface changes, but an explicit
\t// endpoint close must release them even if startup failed partway through.
\tif t.transportConnections != nil {
\t\tdefer t.transportConnections.Close()
\t}
''', "transport teardown")
    transport = replace_once(transport,
        '\tvar reportedAuthURL string\n',
        '\tvar reportedAuthURL string\n\treportedHealth := make(map[string]string)\n', "health warning history")
    transport = replace_once(transport,
        'localBackend.WatchNotifications(t.ctx, ipn.NotifyInitialState|ipn.NotifyPeerPatches, nil,',
        'localBackend.WatchNotifications(t.ctx, ipn.NotifyInitialState|ipn.NotifyPeerPatches|ipn.NotifyInitialHealthState, nil,', "initial health snapshot")
    transport = replace_once(transport, '''\t\t\tif roNotify.ErrMessage != nil {
\t\t\t\tbusError = *roNotify.ErrMessage
\t\t\t\treturn false
\t\t\t}
''', '''\t\t\t// Surface backend health at the normal log level. Low-level tsnet
\t\t\t// logs are trace/debug, so a control failure otherwise looks like
\t\t\t// an endless NoState with no explanation in the app's logs.
\t\t\tif roNotify.Health != nil {
\t\t\t\tcurrent := make(map[string]string, len(roNotify.Health.Warnings))
\t\t\t\tfor code, warning := range roNotify.Health.Warnings {
\t\t\t\t\tcurrent[string(code)] = warning.Text
\t\t\t\t\tif reportedHealth[string(code)] != warning.Text {
\t\t\t\t\t\tif warning.Severity == "low" {
\t\t\t\t\t\t\tt.logger.Info("health: ", warning.Text)
\t\t\t\t\t\t} else {
\t\t\t\t\t\t\tt.logger.Warn("health: ", warning.Text)
\t\t\t\t\t\t}
\t\t\t\t\t}
\t\t\t\t}
\t\t\t\treportedHealth = current
\t\t\t}
\t\t\tif roNotify.ErrMessage != nil {
\t\t\t\tbusError = *roNotify.ErrMessage
\t\t\t\tt.logger.Error("backend: ", busError)
\t\t\t\treturn false
\t\t\t}
''', "backend health logging")
    # Validate every upstream anchor before writing. Never edit GOMODCACHE.
    server.write_text(source)
    monitor.write_text(network)
    endpoint.write_text(transport)
    (root / "protocol/tailscale/sakamoto_transport.go").write_text('''package tailscale

import (
    "context"
    "github.com/sagernet/sing-box/adapter"
    C "github.com/sagernet/sing-box/constant"
    "github.com/sagernet/sing-box/route"
    "github.com/sagernet/sing/common/logger"
    "github.com/sagernet/sing/service"
)

// Only mobile Tailscale transport sockets use this owner. Ordinary proxy
// sockets still obey NetworkManager.ResetNetwork's CloseAll policy. The
// original registry, protection controls, DNS and routing services are kept.
func mobileTransportContext(ctx context.Context, logger logger.ContextLogger) (context.Context, adapter.ConnectionManager) {
    platform := service.FromContext[adapter.PlatformInterface](ctx)
    if platform == nil || !(platform.UnderNetworkExtension() || C.IsAndroid) {
        return ctx, nil
    }
    owner := route.NewConnectionManager(logger)
    child := service.ExtendContext(ctx)
    service.MustRegister[adapter.ConnectionManager](child, owner)
    return child, owner
}
''')
    (root / "daemon/sakamoto_service.go").write_text('''package daemon

import (
    "encoding/json"
    "fmt"
    "github.com/sagernet/sing-box/route/rule"
)

// IsServiceRunning is a gomobile-safe snapshot, including interrupted starts.
func (s *StartedService) IsServiceRunning() bool {
    s.serviceAccess.RLock()
    defer s.serviceAccess.RUnlock()
    return !s.closed && s.instance != nil && s.serviceStatus.Status == ServiceStatus_STARTED
}

// Instance ID prevents a late learning update from modifying a replacement core.
func (s *StartedService) RunningInstanceID() int64 {
    s.serviceAccess.RLock()
    defer s.serviceAccess.RUnlock()
    if s.closed || s.instance == nil || s.serviceStatus.Status != ServiceStatus_STARTED { return 0 }
    return s.startedAt.UnixNano()
}

// UpdateAutomaticDomains serializes with start/stop, then performs only the
// dedicated inline rule-set swap. Existing flows keep their chosen route.
func (s *StartedService) UpdateAutomaticDomains(domainsJSON string, expectedInstanceID int64) error {
    s.lifecycleAccess.Lock()
    defer s.lifecycleAccess.Unlock()
    if expectedInstanceID == 0 || s.RunningInstanceID() != expectedInstanceID { return fmt.Errorf("core changed or stopped") }
    var domains []string
    if err := json.Unmarshal([]byte(domainsJSON), &domains); err != nil { return err }
    instance := s.Instance()
    slot, loaded := instance.Box().Router().RuleSet("sakamoto-auto-proxy")
    if !loaded { return fmt.Errorf("automatic rule-set is unavailable; apply configuration") }
    local, loaded := slot.(*rule.LocalRuleSet)
    if !loaded { return fmt.Errorf("automatic rule-set is not local") }
    return local.UpdateMobileDomains(domains)
}
''')
    (root / "daemon/sakamoto_probe.go").write_text((Path(__file__).resolve().parent / "libbox/sakamoto_probe.go.txt").read_text())
    (root / "route/rule/sakamoto_domains.go").write_text('''package rule

import (
    "fmt"
    "strings"
    "github.com/sagernet/sing-box/option"
    C "github.com/sagernet/sing-box/constant"
    "github.com/sagernet/sing/common/json/badoption"
)

// UpdateMobileDomains uses the same checked, locked swap as rule-set reload.
// The app supplies a dedicated inline slot at startup; arbitrary user rules
// and file-backed sets cannot be mutated through this API.
func (s *LocalRuleSet) UpdateMobileDomains(domains []string) error {
    if s.tag != "sakamoto-auto-proxy" || s.fileFormat != "" || s.watcher != nil {
        return fmt.Errorf("not a mobile automatic domain rule-set")
    }
    if len(domains) > 1000 { return fmt.Errorf("too many automatic domains") }
    for _, domain := range domains {
        if domain == "" || strings.ContainsAny(domain, " /\\\\\\t\\r\\n") {
            return fmt.Errorf("invalid automatic domain")
        }
    }
    var rules []option.HeadlessRule
    if len(domains) > 0 {
        rules = []option.HeadlessRule{{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultHeadlessRule{Domain: badoption.Listable[string](domains)}}}
    }
    return s.reloadRules(rules)
}
''')



if __name__ == "__main__":
    patch(Path(sys.argv[1]))
