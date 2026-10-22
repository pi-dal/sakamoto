# Mobile Tailscale packet forwarding

## Failure cases to verify before implementation

- A leading generic sniff action forces tailnet TCP VNC/SSH into stream forwarding, adding TCP termination and a 300 ms wait for server-first greetings.
- Moving every tailnet rule before sniff can leak TCP/UDP DNS into the endpoint instead of the configured DNS hijacker.
- A packet shortcut can skip a user's conditional/configured sniff or a preceding reject/route/route-options action.
- IPv6, MagicDNS-resolved IPs, and endpoints whose peer map has not arrived can silently miss the packet path.
- Multiple endpoints must keep dynamic route ownership; a static prefix fallback cannot arbitrarily choose one.
- Repeated preparation, old saved profiles, and profile enable/disable cycles can duplicate or retain generated routes.
- Customized preferred-route rules or customized tailnet prefix rules must not be removed as generated rules.
- Non-tailnet TCP/proxy routing and UDP behavior must remain equivalent.
- A fast-path classifier result alone cannot establish successful packet delivery or physical-device VNC throughput.

## Implemented behavior

The runtime preparation boundary already used by the iOS app and extension owns the optimization. Before a leading, unconfigured generic sniff action, insert narrowly matched generated TCP routes for known Tailscale destinations, excluding destination port 53. Keep the existing connectivity routes after sniff for DNS, UDP, and other fallback traffic. Do not add shortcuts across a configured/conditional sniff action or an earlier terminal routing policy. A custom DNS interception rule after sniff also retains the original path; only the default protocol-DNS rule is crossed, with TCP port 53 excluded. Dynamic preferred ownership precedes the single-endpoint literal IPv4/IPv6 fallback.

Rebuild generated rules deterministically and recognize them by their complete shape, retaining customized variants. Adding another endpoint retires any formerly generated single-endpoint fallback. iOS endpoint removal must remove generated connectivity rules, including the new shortcuts, while retaining customized rules. Runtime preparation repairs old profiles as well as newly generated profiles.

## Verification

`scripts/test-libbox-network.py` runs the real pinned core in a private dependency copy. The performance regression fixture records real router classification for IPv4/IPv6 VNC, SSH, TCP/UDP DNS, unrelated Internet destinations, customized policies, repeated preparation and multiple endpoints. A local SOCKS -> real core -> server-first RFB service exchange demonstrates the startup-delay mechanism without physical-device assumptions. Artifacts are written to `SAKAMOTO_TEST_ARTIFACT_DIR`.

```sh
SAKAMOTO_TEST_ARTIFACT_DIR="$PWD/ios/build/vnc-optimization" python3 scripts/test-libbox-network.py
```

The regression was first run against the unmodified preparer and failed for both VNC address families and SSH, then passed after the optimization. The local RFB fixture measured 302.626 ms with sniff first and 0.291 ms with routing first. Those values verify the startup-delay mechanism, not a physical VNC speedup. `NativeTailscaleIntegrationTests.testPreparedTailnetConfigurationCanBeReappliedAndDisabled` exercises Swift preparation through the rebuilt Go bridge and validates the prepared, disabled, and re-enabled configurations with the real Libbox parser.

Physical-device testing is still needed to compare sustained VNC throughput, frame pacing and resource use with official Tailscale on the same phone, access network, Mac tailnet IP and display settings. Unit/classification and local TCP evidence must not be presented as an iPhone throughput result.
