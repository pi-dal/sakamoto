# Importing Shadowrocket configurations

Open `sakamoto`, go to **Config**, then click **导入配置**.

Paste either:

- an `https://` or `http://` URL pointing to a Shadowrocket `.conf`;
- a local `.conf` path.

The importer:

1. validates the source;
2. follows relative `include = ...` entries recursively (up to eight levels);
3. fetches remote `RULE-SET` entries;
4. converts domain/IP rules into sing-box `.srs` rule sets;
5. imports supported proxy/share-link formats;
6. runs `sing-box check`;
7. commits only after validation succeeds.

A failed import leaves the last working `config.json` and rule sets untouched. Remote sources are cached in `~/.sakamoto/imports/`.

## Supported migration

- `DOMAIN`, `DOMAIN-SUFFIX`, `DOMAIN-KEYWORD`, `IP-CIDR`, `GEOIP`, `FINAL` (`FINAL,DIRECT` remains direct; explicit `PROXY` rules still use the chained SOCKS exit);
- `skip-proxy`, `bypass-tun`, DNS servers, DNS hijacking, Tailscale exclusions. List-valued General settings from an included conf are merged without dropping the parent or included values;
- exact `[Host]` IP overrides via a sing-box hosts DNS server (system DNS must actually send the relevant query to sing-box for that override to apply);
- VLESS Reality, VMess, Hysteria2, TUIC, AnyTLS, SOCKS5;
- Shadowrocket's `method:uuid@host` share-link form;
- relative includes and remote rule lists.

## Explicit limitations

Shadowrocket URL/Header/Body Rewrite, MITM decryption, and JavaScript response scripts cannot be reproduced by sing-box's core. The importer reports these instead of silently claiming equivalence.
