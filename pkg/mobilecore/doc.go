// Package mobilecore is the thin public bridge for a future gomobile/SwiftUI
// front end. It re-exposes the platform-agnostic state semantics of
// internal/core using only gomobile-bindable types: string, bool, int32 and
// simple structs with exported string fields.
//
// Design contract:
//
//   - One function per decision the iOS front end must render or act on.
//     All state logic lives in internal/core; this package only converts
//     types and never re-implements a rule.
//   - Values use the internal/core (and therefore docs/tui.md) vocabulary:
//     session phases are Disconnected, Starting, TUNRunning, Reachable,
//     Unverified, Conflict, Unavailable; probe states are Idle, Checking,
//     Reachable, Unverified; node statuses are Untested, Testing, Reachable,
//     Failed; config states are Clean, NeedsRegenerate, NeedsReconnect.
//   - Rendering functions are total: SessionPhase normalizes unknown inputs
//     (unknown service state → "Stopped", unknown probe state → "Idle")
//     because a status label must always render. State-machine functions
//     (ConfigStateTransition) are strict and return an error on unknown
//     input, because a misused transition must be loud.
//   - No Bubble Tea, sing-box daemon, gRPC, or macOS type appears in this
//     package's API or dependency graph (enforced by a test).
//
// Intentionally out of scope for this package: daemon/supervisor transport,
// NetworkExtension plumbing, the bubble-probe HTTP implementation, and any
// configuration I/O. Those belong to platform adapters.
//
// A future iOS build would consume this via, for example:
//
//	gomobile bind -target=ios -o SakamotoCore.xcframework ./pkg/mobilecore
//
// No Xcode project or Libbox binary is part of this repository; this package
// is the real, testable Go foundation only.
package mobilecore
