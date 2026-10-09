import Foundation
import Libbox
import NetworkExtension
import SakamotoKit
import WidgetKit

// Sakamoto PacketTunnelProvider — the iOS tunnel-side integration point.
//
// NOT part of the SwiftPM package and NOT compiled by ios/scripts/verify.sh.
// This file is compiled by the Xcode app project (ios/project.yml, target
// "SakamotoPacketTunnel") together with Libbox.xcframework
// (ios/scripts/build-libbox.sh).
//
// Lifecycle ported from SagerNet/sing-box-for-apple, dev branch:
//   Library/Network/ExtensionProvider.swift
// Source commit: d1224bb5081b3df5d0ecc55b1bd3d72ea6c60628 (2026-10-02).
// Copyright (C) 2022 by nekohasekai <contact-sagernet@sekai.icu>, GPLv3.
// See ExtensionPlatformInterface.swift for the full license header and
// NOTICE.md for the attribution record.
//
// Flow: Setup -> CommandServer -> startOrReloadService(configContent), with
// the sakamoto App <-> provider IPC (SakamotoKit.TunnelRequest/TunnelResponse)
// as the reload/keepalive transport instead of the upstream raw-options
// handleAppMessage.

open class SakamotoPacketTunnelProvider: NEPacketTunnelProvider {
    /// Implements both LibboxPlatformInterfaceProtocol (tun fd, interface
    /// monitor, notifications, TailscaleHostname) and LibboxCommandServer-
    /// HandlerProtocol (serviceStop/ServiceReload/proxy/crash/debug), which
    /// is why the same object is passed as both CommandServer arguments,
    /// exactly like the upstream ExtensionProvider.
    private lazy var platformInterface = ExtensionPlatformInterface(self)

    private var commandServer: LibboxCommandServer?
    private var tunnelStartOptions: TunnelStartOptions?
    @MainActor private var experimentRunner: ProviderExperimentRunner?

    /// Preferences carried through start options (upstream Override-
    /// Preferences). Read by ExtensionPlatformInterface when the core opens
    /// the tun and by includeAllNetworks reporting.
    struct OverridePreferences {
        var includeAllNetworks = false
        var systemProxyEnabled = true
        var excludeDefaultRoute = false
        var autoRouteUseSubRangesByDefault = false
        var excludeAPNsRoute = false
    }

    var overridePreferences: OverridePreferences?

    private enum Paths {
        /// All writable state lives in the app-group container shared with
        /// the containing app. The identifier is read from the target's
        /// Info.plist (key "SakamotoAppGroupIdentifier"); this repository
        /// deliberately does not hardcode one.
        static func sharedDirectory() throws -> String {
            guard let identifier = Bundle.main.object(
                forInfoDictionaryKey: "SakamotoAppGroupIdentifier"
            ) as? String else {
                throw ProviderStartupError(
                    "missing Info.plist key SakamotoAppGroupIdentifier"
                )
            }
            guard let container = FileManager.default.containerURL(
                forSecurityApplicationGroupIdentifier: identifier
            ) else {
                throw ProviderStartupError(
                    "app group \(identifier) is not provisioned for this target"
                )
            }
            return container.path
        }
    }

    struct ProviderStartupError: LocalizedError, CustomStringConvertible {
        let description: String
        init(_ description: String) { self.description = description }
        var errorDescription: String? { description }
    }

    // MARK: Lifecycle (mirrors sing-box-for-apple ExtensionProvider)

    override open func startTunnel(options startOptions: [String: NSObject]?) async throws {
        if startOptions?["configContent"] == nil,
           UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.bool(forKey: "sakamoto.profile.requiresApply") == true {
            throw ProviderStartupError("Open sakamoto and apply the selected configuration before automatic connection.")
        }
        TunnelDiagnostics.clear()
        do { try await startTunnelService(options: startOptions) }
        catch {
            TunnelDiagnostics.record(stage: "VPN startup", error: error)
            await stopExperiments()
            commandServer?.close(); commandServer = nil
            platformInterface.reset()
            throw error
        }
    }

    private func startTunnelService(options startOptions: [String: NSObject]?) async throws {
        let sharedDirectory = try Paths.sharedDirectory()
        let workingDirectory = sharedDirectory + "/Working"
        let cacheDirectory = sharedDirectory + "/Library/Caches"
        try FileManager.default.createDirectory(atPath: workingDirectory, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(atPath: cacheDirectory, withIntermediateDirectories: true)

        // Per-start options win; providerConfiguration is the cold-launch
        // fallback persisted by SakamotoNE.NETunnelController.
        let resolved = TunnelStartOptions(startTunnelOptions: startOptions ?? [:])
            ?? TunnelStartOptions(
                providerConfiguration: (protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration ?? [:]
            )
        guard let options = resolved else {
            throw ProviderStartupError(
                "(sakamoto) missing configContent in tunnel options and provider configuration"
            )
        }
        overridePreferences = OverridePreferences(
            includeAllNetworks: (startOptions?["includeAllNetworks"] as? NSNumber)?.boolValue ?? false,
            systemProxyEnabled: (startOptions?["systemProxyEnabled"] as? NSNumber)?.boolValue ?? true,
            excludeDefaultRoute: (startOptions?["excludeDefaultRoute"] as? NSNumber)?.boolValue ?? false,
            autoRouteUseSubRangesByDefault: (startOptions?["autoRouteUseSubRangesByDefault"] as? NSNumber)?.boolValue ?? false,
            excludeAPNsRoute: (startOptions?["excludeAPNsRoute"] as? NSNumber)?.boolValue ?? false
        )

        let setup = LibboxSetupOptions()
        setup.basePath = sharedDirectory
        setup.workingPath = workingDirectory
        setup.tempPath = cacheDirectory
        setup.logMaxLines = 3000
        setup.debug = false
        setup.crashReportSource = "SakamotoNetworkExtension"
        setup.appVersion = appVersion()
        // Apple NetworkExtension memory ceilings (libbox applies the iOS
        // defaults itself when oomKillerEnabled and no explicit limit is set).
        setup.oomKillerEnabled = true
        var setupError: NSError?
        LibboxSetup(setup, &setupError)
        if let setupError {
            throw ProviderStartupError("(sakamoto) libbox setup: \(setupError.localizedDescription)")
        }

        commandServer = LibboxNewCommandServer(platformInterface, platformInterface, &setupError)
        if let setupError {
            throw ProviderStartupError(
                "(sakamoto) create command server: \(setupError.localizedDescription)"
            )
        }
        try commandServer!.start()

        do {
            try await applyProfile(options)
            SystemSurfaceStore.write(SystemSurfaceSnapshot(serviceState: .running, phase: .tunRunning))
            reloadSystemSurfaces()
        } catch {
            SystemSurfaceStore.write(SystemSurfaceSnapshot(serviceState: .unavailable, phase: .unavailable))
            throw error
        }
        writeTunnelMessage("(packet-tunnel): Here I stand")
    }

    private func reloadSystemSurfaces() {
        WidgetCenter.shared.reloadTimelines(ofKind: "com.pidal.sakamoto.vpn-widget")
        if #available(iOS 18.0, *) {
            ControlCenter.shared.reloadControls(ofKind: "com.pidal.sakamoto.vpn-control")
        }
    }

    override open func stopTunnel(with reason: NEProviderStopReason) async {
        SystemSurfaceStore.write(SystemSurfaceSnapshot(serviceState: .stopping, phase: .stopping))
        defer {
            SystemSurfaceStore.write(SystemSurfaceSnapshot(serviceState: .stopped, phase: .disconnected))
            reloadSystemSurfaces()
        }
        await stopExperiments()
        writeTunnelMessage("(packet-tunnel) stopping, reason: \(reason.rawValue)")
        do {
            try commandServer?.closeService()
        } catch {
            // 2 = error log level (LibboxCommandClient log levels).
            commandServer?.writeMessage(2, message: "\(error)")
        }
        platformInterface.reset()
        if let commandServer {
            try? await Task.sleep(nanoseconds: 100 * NSEC_PER_MSEC)
            commandServer.close()
            self.commandServer = nil
        }
    }

    override open func handleAppMessage(_ messageData: Data) async -> Data? {
        let request: TunnelRequest
        do {
            request = try TunnelRequest(data: messageData)
        } catch {
            return try? TunnelResponse.failure("bad request: \(error)").encode()
        }

        switch request {
        case .ping:
            let snapshot = TunnelStateSnapshot(
                serviceState: commandServer != nil ? .running : .unavailable,
                detail: nil
            )
            return try? TunnelResponse(ok: true, state: snapshot).encode()
        case .recoverExperiment:
            do { try await recoverExperiment(); return try? TunnelResponse(ok: true).encode() }
            catch { return try? TunnelResponse.failure(TunnelDiagnostics.sanitized(error.localizedDescription)).encode() }
        case .removeLearnedDomain(let domain):
            do { try await removeLearned(domain); return try? TunnelResponse(ok: true).encode() }
            catch { return try? TunnelResponse.failure(TunnelDiagnostics.sanitized(error.localizedDescription)).encode() }
        case .reloadProfile(let options):
            do {
                try await applyProfile(options)
                tunnelStartOptions = options
                TunnelDiagnostics.clear()
                return try? TunnelResponse(ok: true).encode()
            } catch {
                TunnelDiagnostics.record(stage: "VPN reload", error: error)
                return try? TunnelResponse.failure(TunnelDiagnostics.sanitized(error.localizedDescription)).encode()
            }
        case .reloadConfig(let content):
            do {
                var options = tunnelStartOptions ?? TunnelStartOptions(configContent: content)
                options.configContent = content
                try await applyProfile(options)
                tunnelStartOptions = options
                TunnelDiagnostics.clear()
                return try? TunnelResponse(ok: true).encode()
            } catch {
                TunnelDiagnostics.record(stage: "VPN reload", error: error)
                return try? TunnelResponse.failure(TunnelDiagnostics.sanitized(error.localizedDescription)).encode()
            }
        }
    }

    override open func sleep() async {
        commandServer?.pause()
    }

    override open func wake() {
        commandServer?.wake()
    }

    // MARK: Service control

    /// startOrReloadService with the generated sing-box config. Used by both
    /// startTunnel and the reloadConfig provider message — a regenerated
    /// config is applied without tearing down the tunnel, exactly like the
    /// official reload path (ConfigState: NeedsReconnect -> applied).
    @MainActor private func stopExperiments() async {
        await experimentRunner?.quiesce(); experimentRunner = nil
    }

    @MainActor private func recoverExperiment() throws {
        guard let runner = experimentRunner else { throw ProviderStartupError("Experiment is unavailable; apply a profile first") }
        try runner.beginRecovery(manual: true)
    }
    @MainActor private func removeLearned(_ domain: String) async throws {
        guard let runner = experimentRunner else { throw ProviderStartupError("No running Experiment profile") }
        try await runner.remove(domain)
    }
    @MainActor private func applyProfile(_ options: TunnelStartOptions) async throws {
        let root = URL(fileURLWithPath: try Paths.sharedDirectory())
        let (content, settings) = try ProviderExperimentRunner.prepared(options, root: root)
        // Preflight before closing the previous monitor or service.
        var error: NSError?
        _ = LibboxCheckConfig(content, &error)
        if let error { throw error }
        let previous = tunnelStartOptions
        await stopExperiments()
        do {
            try await startService(content: content, forceDERP: options.forceTailscaleDERP == true)
            try startExperiments(options: options, settings: settings, content: content, root: root)
            tunnelStartOptions = options
        } catch {
            // startOrReloadService can close the old service before rejecting
            // its replacement. Restore both runtime and monitor, not just NE
            // preferences in the app process.
            if let previous {
                do {
                    await stopExperiments()
                    let (oldContent, oldSettings) = try ProviderExperimentRunner.prepared(previous, root: root)
                    try await startService(content: oldContent, forceDERP: previous.forceTailscaleDERP == true)
                    try startExperiments(options: previous, settings: oldSettings, content: oldContent, root: root)
                } catch {
                    TunnelDiagnostics.record(stage: "VPN reload rollback", error: error)
                    throw ProviderStartupError("VPN reload rollback failed; disconnect and reconnect")
                }
            }
            throw error
        }
    }

    @MainActor private func startExperiments(options: TunnelStartOptions, settings: ExperimentSettings?, content: String, root: URL) throws {
        guard let id = options.profileID, let settings else { return }
        let runner = try ProviderExperimentRunner(profileID: id, settings: settings, content: content, root: root) { [weak self] candidate in
            guard let self else { throw ProviderStartupError("Provider stopped") }
            try await self.startService(content: candidate, forceDERP: options.forceTailscaleDERP == true)
        }
        experimentRunner = runner; runner.start()
    }

    @MainActor private func startService(content: String, forceDERP: Bool) async throws {
        guard let commandServer else {
            throw ProviderStartupError("(sakamoto) command server not started")
        }
        // Libbox's synchronous start calls openTun, which blocks while its
        // async network-settings callback runs. Never hold the main actor
        // across that native call (or starve lifecycle/IPC callbacks).
        try await Task.detached(priority: .userInitiated) {
            if MobilegenTailscaleForceDERPEnabled() != forceDERP {
                // Rebuild peer sockets only after closing the previous service.
                // An atomic policy alone cannot close an already-direct socket.
                try commandServer.closeService()
                MobilegenSetTailscaleForceDERP(forceDERP)
            }
            try commandServer.startOrReloadService(content, options: LibboxOverrideOptions())
        }.value
    }

    /// CommandServerHandler.serviceStop entry point (ExtensionPlatformInterface).
    func stopTunnelService() {
        do {
            try commandServer?.closeService()
        } catch {
            writeTunnelMessage("(packet-tunnel) stop service: \(error.localizedDescription)")
        }
        platformInterface.reset()
    }

    /// CommandServerHandler.serviceReload entry point (ExtensionPlatformInterface).
    func reloadTunnelService() async throws {
        writeTunnelMessage("(packet-tunnel) reloading service")
        reasserting = true
        defer { reasserting = false }
        guard let content = tunnelStartOptions?.configContent else {
            throw ProviderStartupError("(sakamoto) no config content to reload")
        }
        var options = tunnelStartOptions ?? TunnelStartOptions(configContent: content)
        options.configContent = content
        try await applyProfile(options)
    }

    /// Error-level message into the libbox log ring (upstream writeMessage).
    func writeTunnelMessage(_ message: String) {
        commandServer?.writeMessage(2, message: message)
    }

    private func appVersion() -> String {
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String
        return [version, build].compactMap { $0 }.joined(separator: " (")
            + (build != nil ? ")" : "")
    }
}
