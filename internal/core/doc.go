// Package core holds the platform-agnostic domain model shared by the macOS
// TUI and a future iOS front end.
//
// The package expresses the state vocabulary documented in docs/tui.md:
//
//   - Connection lifecycle (ServiceState) and the combined status-bar phase
//     (SessionPhase): Disconnected, Starting, TUNRunning, Reachable,
//     Unverified, Conflict, Unavailable.
//   - Network probe state (ProbeState): TUN running is not network reachable,
//     and a single failed probe is Unverified, never a verdict of an outage.
//   - Routing mode (RoutingMode): Rule → Global → Direct, validated against
//     the modes the running core actually exposes.
//   - Node and group status (NodeState, GroupState): a filled dot means
//     selected, never reachable (skills.md invariant 9).
//   - Configuration change state (ConfigState): edits need Regenerate, a
//     regenerated config needs Reconnect before the core uses it.
//   - User notification (Notice): the transient status line.
//
// Boundary rules enforced here and tested in this package:
//
//   - Selected ≠ Reachable.
//   - TUNRunning ≠ Reachable; Reachable requires a running TUN *and* a passed
//     probe, so a stale probe result can never survive a disconnect.
//   - One failed probe flips Reachable back to Unverified with a bounded
//     retry backoff; the UI must not declare the network down.
//   - Any saved configuration modification requires regenerate; only a
//     completed regeneration plus reconnect returns to Clean.
//
// The package deliberately imports nothing from Bubble Tea, lipgloss, the
// sing-box daemon API, gRPC, or any macOS service (scutil, launchd,
// NetworkExtension). Platform adapters convert native observations into these
// values at the edges; the state semantics stay identical on every platform.
package core
