import AppIntents

// On iOS 16.4–25, these conformances keep execution in the containing app
// without foregrounding it. WidgetKit compiles the shared intent definitions;
// this app-only file supplies the unavailable-in-extensions conformance.
// On iOS 26+, supportedModes(.foreground(.dynamic)) is the replacement.
@available(iOS 16.4, *)
extension ConnectTunnelIntent: ForegroundContinuableIntent {}

@available(iOS 16.4, *)
extension DisconnectTunnelIntent: ForegroundContinuableIntent {}

@available(iOS 16.4, *)
extension ToggleTunnelIntent: ForegroundContinuableIntent {}

@available(iOS 16.4, *)
extension TunnelStatusIntent: ForegroundContinuableIntent {}

@available(iOS 18.0, *)
extension SetTunnelEnabledIntent: ForegroundContinuableIntent {}
