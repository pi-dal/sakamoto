import Foundation
import Libbox
import NetworkExtension
import SakamotoKit

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
        let sharedDirectory = try Paths.sharedDirectory()
        let workingDirectory = sharedDirectory + "/Working"
        let cacheDirectory = sharedDirectory + "/Library/Caches"
        try? FileManager.default.createDirectory(
            atPath: workingDirectory, withIntermediateDirectories: true
        )

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
        tunnelStartOptions = options
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
            try await startService(content: options.configContent)
            SystemSurfaceStore.write(SystemSurfaceSnapshot(serviceState: .running, phase: .tunRunning))
        } catch {
            SystemSurfaceStore.write(SystemSurfaceSnapshot(serviceState: .unavailable, phase: .unavailable))
            throw error
        }
        writeTunnelMessage("(packet-tunnel): Here I stand")
    }

    override open func stopTunnel(with reason: NEProviderStopReason) async {
        SystemSurfaceStore.write(SystemSurfaceSnapshot(serviceState: .stopping, phase: .stopping))
        defer { SystemSurfaceStore.write(SystemSurfaceSnapshot(serviceState: .stopped, phase: .disconnected)) }
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
        case .reloadConfig(let content):
            do {
                try await startService(content: content)
                return try? TunnelResponse(ok: true).encode()
            } catch {
                return try? TunnelResponse.failure("\(error)").encode()
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
    private func startService(content: String) async throws {
        guard let commandServer else {
            throw ProviderStartupError("(sakamoto) command server not started")
        }
        let override = LibboxOverrideOptions()
        try commandServer.startOrReloadService(content, options: override)
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
        try await startService(content: content)
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
