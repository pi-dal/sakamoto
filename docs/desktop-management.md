# Desktop learning and source editing

## Failure cases before implementation

- Opening a page must not generate configuration, start a watcher, or connect a tunnel.
- Malformed learning history must be reported, never treated as an empty successful history.
- Only recent reports with an explicitly failed direct probe and a valid domain can be selected; recovered direct checks and invalid/IP targets are excluded.
- Stale selections must be revalidated against current history before saving.
- Duplicate exact rules must be skipped regardless of action, preserving explicit direct/reject overrides.
- Invalid policy matches, subscription URLs, and node links must leave saved sources intact.
- Editing a stale collection must fail rather than overwrite concurrent changes.
- Share links and subscription URLs must remain masked unless explicitly revealed in an editor; lists and notices must not expose credentials.
- Node edits must preserve comments and other lines, using the existing atomic node writer. Field edits must preserve credentials, raw query options, VMess JSON extensions, encoded authorities, IPv6 and hopping-port ranges; invalid hosts and ports must fail without saving.
- Saving one collection must preserve other settings and API credentials, and leave generated config and running service untouched.
- Empty lists, search with no results, busy saves, failed saves, and Back/Cancel must remain usable in the 400 × 640 panel.

## Workflow

Settings exposes Learning and Nodes & sources. Configuration also links to Learning, Nodes & sources, and Routing policy. Each page uses the existing panel navigation, search, accessible controls and light/dark palette.

Learning displays saved mode, learned route count and the latest twenty checks. Selecting a domain adds an exact proxy rule to the sidecar policy after revalidation, without changing the active runtime. Existing exact overrides are retained. Apply saved changes through Check & repair or the Apply saved configuration action on Nodes & sources and Routing policy (external-core users apply through their service).

Nodes, subscriptions and policy rules have searchable lists and in-panel add/edit/remove forms. Existing nodes expose remark, server and single-port fields without exposing credentials; full-link replacement remains available for advanced options or malformed entries. Hopping ports are retained. Sensitive link fields are masked by default. Saves validate inputs and reject stale source revisions. Node files retain comments and unrelated lines.

## Applying saved policy

Saving a rule only writes the source configuration. On Routing policy, choose **Apply saved configuration…**, then confirm **Regenerate and reconnect**. When disconnected, the confirmation says **Regenerate and connect**. Preparation validates a private candidate before changing the active configuration. A failed generation leaves the existing runtime intact.

A supervisor without configuration recovery exposes **Update service and apply…**. This opens the bundled CLI in Terminal with `service-update --reconnect --regenerate`; the terminal requests confirmation and local administrator authorization before disconnecting. The update replaces both the identified supervisor and the current user's matching watcher. It verifies the replacement watcher's version, protocol and PID before replacing the supervisor, then regenerates saved settings and verifies the new core API and protected DNS. The explicit regenerate option also connects an initially disconnected VPN; the terminal explains this before confirmation. Plain `service-update --reconnect` preserves an initially disconnected state.

## Repeatable evidence

Run `rtk proxy python3 scripts/check-desktop.py --race`. With `SAKAMOTO_MENUBAR_ARTIFACT_DIR` set, integration workflows retain JSON receipts and light/dark MyGo content renders. These are isolated source/controller and input-driven UI fixtures; they do not use owner configuration or a real TUN. The policy application workflow retains `policy-apply-e2e.json` and light/dark captures of saved, confirmation and legacy-service states. `mise run go:recovery` includes watcher replacement/rollback fixtures and checks the policy inside the compiled rules referenced by a real native core process.
