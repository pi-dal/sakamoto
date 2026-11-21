#!/usr/bin/env python3
"""Check the installed core against generated synthetic profiles, offline."""
import base64
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
from pathlib import Path
import secrets
import subprocess
import tempfile
from threading import Thread


def check_redirects(tool, settings, conf):
    original = settings.read_text()
    servers = []
    seen = []

    class Source(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            seen.append((self.server.server_port, self.path, self.headers.get("Referer")))
            if self.path == "/clash":
                self.send_response(200)
                self.end_headers()
                self.wfile.write(json.dumps({"proxies": [{"type": "hysteria2", "name": "fixture", "server": "192.0.2.40", "port": 443, "fingerprint": "ab"*32}]}).encode())
            elif self.path.startswith("/token-"):
                self.send_response(302)
                target = "/rules" if self.path.endswith("same") else f"http://127.0.0.1:{servers[1].server_port}/rules"
                self.send_header("Location", target)
                self.end_headers()
            else:
                self.send_response(200)
                self.end_headers()
                self.wfile.write(conf.read_bytes())

    try:
        for _ in range(2):
            server = HTTPServer(("127.0.0.1", 0), Source)
            servers.append(server)
            Thread(target=server.serve_forever, daemon=True).start()
        for case in ("same", "cross"):
            seen.clear()
            url = f"http://127.0.0.1:{servers[0].server_port}/token-{case}"
            settings.write_text(original.replace(f"conf: {conf}", f"conf: {url}"))
            result = subprocess.run([tool, settings], capture_output=True, text=True, timeout=30)
            assert (result.returncode == 0) == (case == "same"), case
            assert "/token-" not in result.stdout + result.stderr
            expected = [f"/token-{case}", "/rules"] if case == "same" else [f"/token-{case}"]
            assert [path for _, path, _ in seen] == expected
            assert all(port == servers[0].server_port and referer is None for port, _, referer in seen)
        settings.write_text(original + f"subscriptions:\n  - name: fixture\n    format: clash\n    url: http://127.0.0.1:{servers[0].server_port}/clash\n")
        rejected = subprocess.run([tool, settings], capture_output=True, text=True)
        assert rejected.returncode != 0 and "fingerprint" in rejected.stderr and "ab"*32 not in rejected.stderr
    finally:
        settings.write_text(original)
        for server in servers:
            server.shutdown()
            server.server_close()


def main():
    with tempfile.TemporaryDirectory(prefix="sakamoto-privacy-") as work:
        root = Path(work)
        tool = root / "generate"
        subprocess.run(["go", "build", "-o", tool, "tools/main.go"], check=True)
        conf = root / "input.conf"
        nodes = root / "nodes.txt"
        nodes.write_text("trojan://fixture@192.0.2.30:443?sni=relay.example#Relay\n")
        vmess = {"add": "192.0.2.32", "port": "443", "ps": "vmess-fixture", "id": "7d81c26e-cd4f-44d8-bfec-10ad4627f59a", "net": "tcp", "tls": "tls", "sni": "vm.example"}
        with nodes.open("a") as f:
            f.write("vmess://" + base64.b64encode(json.dumps(vmess).encode()).decode() + "\n")
            nested = base64.urlsafe_b64encode(b"https://example.test/private-marker").decode().rstrip("=")
            f.write(f"sub://{nested}\n")
        settings = root / "sakamoto.yaml"
        settings.write_text(f"api:\n  url: http://127.0.0.1:9090\n  secret: {secrets.token_hex(32)}\nconf: {conf}\nnodes_file: {nodes}\nchain_enabled: false\nexperiment:\n  mode: off\n  threshold: 3\ntailscale_optimize: false\n")
        for final, expected in [("PROXY", "MainProxy"), ("DIRECT", "direct")]:
            conf.write_text(f"[General]\ndns-server = 192.0.2.53#proxy\nfallback-dns-server = tls://192.0.2.54\nexample-option = private-marker\n[MITM]\nca-passphrase = private-marker\n[Rule]\nDOMAIN,proxied.overlap.test,PROXY\nDOMAIN-SUFFIX,overlap.test,DIRECT\nDOMAIN,local.example,DIRECT\nDOMAIN,ads.example,REJECT\nDOMAIN,proxied.example,PROXY\nFINAL,{final}\n")
            generated = subprocess.run([tool, settings], check=True, capture_output=True, text=True)
            core = json.loads((root / "config.json").read_text())
            tun = next(i for i in core["inbounds"] if i["type"] == "tun")
            assert any(":" in a for a in tun["address"]), "missing IPv6 capture"
            assert next(s for s in core["dns"]["servers"] if s.get("server") == "192.0.2.53")["detour"] == "MainProxy"
            tls = next(o for o in core["outbounds"] if o.get("tag") == "vmess-fixture")["tls"]
            assert tls["enabled"] and tls["server_name"] == "vm.example"
            assert core["route"]["final"] == expected, f"incorrect FINAL,{final}"
            assert {s["type"] for s in core["dns"]["servers"] if s.get("server") == "192.0.2.54"} == {"tls"}
            assert all("192.0.2.30/32" not in i.get("route_exclude_address", []) for i in core["inbounds"])
            assert "private-marker" not in generated.stdout + generated.stderr
            assert "skipped 1 nested subscriptions" in generated.stdout
            direct_rule = next(r for r in core["dns"]["rules"] if r.get("clash_mode") == "Rule" and "rule_set" in r)
            direct_set = next(s for s in core["route"]["rule_set"] if s["tag"] in direct_rule["rule_set"])
            for host, expected_match in [("local.overlap.test", True), ("proxied.overlap.test", False)]:
                match = subprocess.run(["sing-box", "rule-set", "match", "-f", "binary", root / direct_set["path"], host], check=True, capture_output=True, text=True)
                assert ("match rules.[" in match.stdout + match.stderr) == expected_match, host
        route_probe = root / "route_probe.go"
        route_probe.write_text('''package main
import ("encoding/json"; "fmt"; "net/netip"; "os"; tun "github.com/sagernet/sing-tun")
func main() {
    var core struct { Inbounds []struct{ Type string; Address []netip.Prefix } }
    raw, err := os.ReadFile(os.Args[1]); if err != nil { panic(err) }
    if err := json.Unmarshal(raw, &core); err != nil { panic(err) }
    opts := tun.Options{AutoRoute:true}
    for _, i := range core.Inbounds { if i.Type == "tun" { for _, a := range i.Address {
        if a.Addr().Is6() { opts.Inet6Address = append(opts.Inet6Address,a) } else { opts.Inet4Address = append(opts.Inet4Address,a) }
    } } }
    ranges,err := opts.BuildAutoRouteRanges(false); if err != nil { panic(err) }
    for _, sample := range []string{"192.0.2.15", "2001:db8::15"} {
        covered := false
        for _, r := range ranges { if r.Contains(netip.MustParseAddr(sample)) { covered = true } }
        if !covered { panic("public address outside TUN routes") }
    }
    fmt.Println("Both IP families covered by native route builder")
}
''')
        subprocess.run(["go", "run", route_probe, root / "config.json"], check=True)
        conf.write_text("[General]\ndns-server = https://192.0.2.53:8443/tenant%2Fprivate-marker#proxy\n[Rule]\nFINAL,PROXY\n")
        result = subprocess.run([tool, settings], check=True, capture_output=True, text=True)
        servers = json.loads((root / "config.json").read_text())["dns"]["servers"]
        doh = [s for s in servers if s["tag"] in {"remote0", "mode-global-dns"}]
        assert len(doh) == 2 and all(s["server_port"] == 8443 and s["path"] == "/tenant%2Fprivate-marker" for s in doh)
        assert "private-marker" not in result.stdout + result.stderr
        for url in ["https://192.0.2.53/q?tenant=private-marker", "https://private-marker@192.0.2.53/q", "https://192.0.2.53/q#private-marker", "https://192.0.2.53:private-marker/q"]:
            conf.write_text(f"[General]\ndns-server = {url}\n[Rule]\nFINAL,PROXY\n")
            rejected = subprocess.run([tool, settings], capture_output=True, text=True)
            assert rejected.returncode != 0 and "private-marker" not in rejected.stdout + rejected.stderr
        conf.write_text("[Rule]\nFINAL,PROXY\n")
        check_redirects(tool, settings, conf)
        original = settings.read_text()
        legacy = root / "legacy.json"
        host = "192.0.2.39"
        node = {"host": host, "title": "legacy", "type": "VMess", "uuid": vmess["id"], "port": "443", "tls": True, "peer": "legacy.example"}
        legacy.write_text(json.dumps([node]))
        settings.write_text(original + f"srjson: {legacy}\nallow_hosts: [{host}]\n")
        subprocess.run([tool, settings], check=True, capture_output=True)
        core = json.loads((root / "config.json").read_text())
        imported = next(o for o in core["outbounds"] if o["tag"] == "legacy")
        assert imported["tls"] == {"enabled": True, "server_name": "legacy.example"}
        legacy.write_text(json.dumps([dict(node, tls="unsupported")]))
        assert subprocess.run([tool, settings], capture_output=True).returncode != 0
        settings.write_text(original + "dns_guard:\n  enabled: true\n")
        for resolver in ["", "192.168.12.53"]:
            servers = ",".join(filter(None, ["https://resolver.example/dns-query#proxy", "1.1.1.1", resolver]))
            conf.write_text(f"[General]\ndns-server = {servers}\n[Rule]\nFINAL,PROXY\n")
            subprocess.run([tool, settings], check=True, capture_output=True)
            dns = json.loads((root / "config.json").read_text())["dns"]
            rule = next(r for r in dns["rules"] if "lan" in r.get("domain_suffix", []))
            if resolver:
                assert next(s for s in dns["servers"] if s["tag"] == rule["server"])["server"] == resolver
            else:
                assert rule["action"] == "predefined" and rule["rcode"] == "SERVFAIL" and "server" not in rule
        for scheme in ["https", "tls"]:
            conf.write_text(f"[General]\ndns-server = {scheme}://resolver.lan#proxy,192.168.12.53\n[Rule]\nFINAL,PROXY\n")
            subprocess.run([tool, settings], check=True, capture_output=True)
            servers = json.loads((root / "config.json").read_text())["dns"]["servers"]
            private = next(s["tag"] for s in servers if s.get("server") == "192.168.12.53")
            assert all(s["domain_resolver"] == private for s in servers if s.get("server") == "resolver.lan")
            conf.write_text(conf.read_text().replace(",192.168.12.53", ""))
            assert subprocess.run([tool, settings], capture_output=True).returncode != 0
        settings.write_text(original)
        conf.write_text("[Rule]\nFINAL,PROXY\n")
        original_nodes = nodes.read_bytes()
        with nodes.open("a") as f:
            f.write("hy2://synthetic@192.0.2.10:443?pinSHA256=" + "ab"*32 + "#pinned\n")
        rejected = subprocess.run([tool, settings], capture_output=True, text=True)
        assert rejected.returncode != 0 and "pinSHA256" in rejected.stderr and "ab"*32 not in rejected.stderr
        nodes.write_bytes(original_nodes)
        for invalid in ["FINAL,PROXY\nDOMAIN,later.example,DIRECT", "IP-ASN,64496,PROXY\nFINAL,DIRECT", "FINAL,PROXY\nFINAL,DIRECT"]:
            conf.write_text("[Rule]\n" + invalid + "\n")
            assert subprocess.run([tool, settings], capture_output=True).returncode != 0
        print("Core validation, redaction and same/cross-origin redirect checks passed")


if __name__ == "__main__":
    main()
