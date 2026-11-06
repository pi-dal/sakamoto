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

Learning displays saved mode, learned route count and the latest twenty checks. Selecting a domain adds an exact proxy rule to the sidecar policy after revalidation, without changing the active runtime. Existing exact overrides are retained. Apply saved changes through the existing Check & repair regeneration flow (external-core users apply through their service).

Nodes, subscriptions and policy rules have searchable lists and in-panel add/edit/remove forms. Existing nodes expose remark, server and single-port fields without exposing credentials; full-link replacement remains available for advanced options or malformed entries. Hopping ports are retained. Sensitive link fields are masked by default. Saves validate inputs and reject stale source revisions. Node files retain comments and unrelated lines.

## Repeatable evidence

Run `rtk proxy python3 scripts/check-desktop.py --race`. With `SAKAMOTO_MENUBAR_ARTIFACT_DIR` set, integration workflows retain JSON receipts and light/dark MyGo content renders. These are isolated source/controller and input-driven UI fixtures; they do not use owner configuration or a real TUN.
