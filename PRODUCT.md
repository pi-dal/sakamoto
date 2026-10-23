# Product

<!-- impeccable:product-schema 1 -->

## Platform

adaptive

The existing mobile clients remain native. Desktop now shares a tray client
across macOS, Windows and Linux, with platform-specific shell/service adapters.

## Stack

Existing Go core, supervisor and Bubble Tea TUI. The user approved implementing the researched menu bar GUI with MyGo native UI and evaluating Onevra. Desktop rendering dependencies live in a separate Go module.

## Users

Desktop users who need to observe and control their sakamoto connection without keeping a terminal open.

## Product Purpose

Control an existing tunnel, choose routing modes and nodes, and distinguish a running tunnel from verified network reachability.

## Operating Context

A resident tray utility supplements the existing CLI/TUI. On macOS, the independently running supervisor owns privileged TUN and DNS operations; the watcher owns automatic fallback. Windows/Linux use an existing core API, with connection lifecycle and DNS owned by that external service. Opening the app never connects.

## Capabilities and Constraints

The shared desktop surface has observed connection/API state, Rule/Global/Direct modes, node search and selection, latency tests, compact traffic, and in-panel settings. macOS adds supervisor connection control and a bundled TUI; Windows/Linux expose external ownership and a configuration folder instead of unsupported lifecycle actions. Reuse existing configuration and credentials. Selection does not imply reachability. A hidden panel has no observation polling, subscriptions, conflict subprocesses or network probes; reopening refreshes before enabling connection actions. macOS exposes read-only health checks, repair through the existing supervisor, explicitly confirmed reconnect and bounded watcher recovery requests. Watcher acceptance is distinct from verified reachability; maintenance and external-service ownership remain intact. An unavailable supervisor offers an explicit bundled repair terminal action. Closing a tray panel keeps the app resident; quitting preserves the independent service. Login launch is opt-in and never connects. Linux uses an AppIndicator menu and compositor-managed panel; a missing tray has a normal-window fallback.

## Brand Commitments

Preserve the sakamoto name and existing GPL-3.0-or-later licensing. Use concise operational copy and familiar platform behavior.

## Evidence on Hand

`README.md`, `docs/tui.md`, `docs/menubar-gui-research.md`, `internal/core`, `internal/svc`, `internal/security` and `internal/sbclient` establish existing behavior. Demo/test scenarios must be explicitly labeled and must not use private runtime data.

## Product Principles

- Reuse one connection state vocabulary across interfaces.
- Show observed state rather than optimistic connectivity claims.
- Keep routine control accessible in the menu bar.
- Keep privileged service and application lifecycles independent.

## Accessibility & Inclusion

Controls need meaningful accessible labels, keyboard navigation, system appearance support and readable status text independent of color. Chinese input must remain possible in node search.
