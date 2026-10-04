import Foundation
import Libbox
import NetworkExtension
import UserNotifications

// Ported (iOS subset) from SagerNet/sing-box-for-apple, dev branch:
//   Library/Network/ExtensionPlatformInterface.swift
// Source commit: d1224bb5081b3df5d0ecc55b1bd3d72ea6c60628 (2026-10-02).
//
// Copyright (C) 2022 by nekohasekai <contact-sagernet@sekai.icu>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.
//
// iOS-only trim, deliberately (see ios/README.md "Boundaries"):
//   - kept: OpenTun over packetFlow, NWPathMonitor interface monitor,
//     WIFI state, DNS cache reset, system proxy settings bridge,
//     user notifications, TailscaleHostname (needed by the built-in
//     Tailscale endpoint to name this device on the tailnet).
//   - dropped as explicit `throw`/`false` (upstream behaves the same on
//     iOS): connection-owner lookup, shell/SFTP/SSH-agent, platform
//     bridge, neighbor monitor, user lookup. macOS XPC / system-extension
//     / jailbreak branches are not carried over at all.
//
// Signatures follow the Libbox v1.14.2 gomobile bindings (LibboxPlatform-
// InterfaceProtocol + LibboxCommandServerHandlerProtocol), which differ
// from the dev branch the upstream file targets (e.g. v1.14.2 exposes
// SendNotification/CancelNotification and UsePlatformAutoDetectInterface-
// Control rather than Send/Cancel/UsePlatformAutoDetectControl).

final class ExtensionPlatformInterface: NSObject, LibboxPlatformInterfaceProtocol, LibboxCommandServerHandlerProtocol {
    private let tunnel: SakamotoPacketTunnelProvider
    private var networkSettings: NEPacketTunnelNetworkSettings?

    init(_ tunnel: SakamotoPacketTunnelProvider) {
        self.tunnel = tunnel
    }

    // MARK: OpenTun (translated from the upstream port verbatim)

    func openTun(_ options: LibboxTunOptionsProtocol?, ret0_: UnsafeMutablePointer<Int32>?) throws {
        try runBlocking { [self] in
            try await openTun0(options, ret0_)
        }
    }

    private func openTun0(_ options: LibboxTunOptionsProtocol?, _ ret0_: UnsafeMutablePointer<Int32>?) async throws {
        guard let options else {
            throw NSError(domain: "ExtensionPlatformInterface", code: 0, userInfo: [NSLocalizedDescriptionKey: "Nil options"])
        }
        guard let ret0_ else {
            throw NSError(domain: "ExtensionPlatformInterface", code: 0, userInfo: [NSLocalizedDescriptionKey: "Nil return pointer"])
        }

        let prefs = tunnel.overridePreferences
        let autoRouteUseSubRangesByDefault = prefs?.autoRouteUseSubRangesByDefault ?? false
        let excludeAPNs = prefs?.excludeAPNsRoute ?? false
        let excludeDefaultRoute = prefs?.excludeDefaultRoute ?? false
        let systemProxyEnabled = prefs?.systemProxyEnabled ?? true

        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
        if options.getAutoRoute() {
            settings.mtu = NSNumber(value: options.getMTU())

            var dnsSettings: NEDNSSettings?
            if options.getDNSMode()!.value != LibboxDNSModeDisabled {
                let dnsServerIterator = try options.getDNSServerAddress()
                var dnsServers: [String] = []
                while dnsServerIterator.hasNext() {
                    dnsServers.append(dnsServerIterator.next())
                }
                if !dnsServers.isEmpty {
                    let newDNSSettings = NEDNSSettings(servers: dnsServers)
                    settings.dnsSettings = newDNSSettings
                    dnsSettings = newDNSSettings
                }
            }

            var ipv4Address: [String] = []
            var ipv4Mask: [String] = []
            let ipv4AddressIterator = options.getInet4Address()!
            while ipv4AddressIterator.hasNext() {
                let ipv4Prefix = ipv4AddressIterator.next()!
                ipv4Address.append(ipv4Prefix.address())
                ipv4Mask.append(ipv4Prefix.mask())
            }

            let ipv4Settings = NEIPv4Settings(addresses: ipv4Address, subnetMasks: ipv4Mask)
            var ipv4Routes: [NEIPv4Route] = []
            var ipv4ExcludeRoutes: [NEIPv4Route] = []

            let inet4RouteAddressIterator = options.getInet4RouteAddress()!
            if inet4RouteAddressIterator.hasNext() {
                while inet4RouteAddressIterator.hasNext() {
                    let ipv4RoutePrefix = inet4RouteAddressIterator.next()!
                    ipv4Routes.append(NEIPv4Route(destinationAddress: ipv4RoutePrefix.address(), subnetMask: ipv4RoutePrefix.mask()))
                }
            } else if autoRouteUseSubRangesByDefault {
                ipv4Routes.append(NEIPv4Route(destinationAddress: "1.0.0.0", subnetMask: "255.0.0.0"))
                ipv4Routes.append(NEIPv4Route(destinationAddress: "2.0.0.0", subnetMask: "254.0.0.0"))
                ipv4Routes.append(NEIPv4Route(destinationAddress: "4.0.0.0", subnetMask: "252.0.0.0"))
                ipv4Routes.append(NEIPv4Route(destinationAddress: "8.0.0.0", subnetMask: "248.0.0.0"))
                ipv4Routes.append(NEIPv4Route(destinationAddress: "16.0.0.0", subnetMask: "240.0.0.0"))
                ipv4Routes.append(NEIPv4Route(destinationAddress: "32.0.0.0", subnetMask: "224.0.0.0"))
                ipv4Routes.append(NEIPv4Route(destinationAddress: "64.0.0.0", subnetMask: "192.0.0.0"))
                ipv4Routes.append(NEIPv4Route(destinationAddress: "128.0.0.0", subnetMask: "128.0.0.0"))
            } else {
                ipv4Routes.append(NEIPv4Route.default())
            }

            let inet4RouteExcludeAddressIterator = options.getInet4RouteExcludeAddress()!
            while inet4RouteExcludeAddressIterator.hasNext() {
                let ipv4RoutePrefix = inet4RouteExcludeAddressIterator.next()!
                ipv4ExcludeRoutes.append(NEIPv4Route(destinationAddress: ipv4RoutePrefix.address(), subnetMask: ipv4RoutePrefix.mask()))
            }
            if excludeDefaultRoute, !ipv4Routes.isEmpty {
                if !ipv4ExcludeRoutes.contains(where: { it in
                    it.destinationAddress == "0.0.0.0" && it.destinationSubnetMask == "255.255.255.254"
                }) {
                    ipv4ExcludeRoutes.append(NEIPv4Route(destinationAddress: "0.0.0.0", subnetMask: "255.255.255.254"))
                }
            }
            if excludeAPNs, !ipv4Routes.isEmpty {
                if !ipv4ExcludeRoutes.contains(where: { it in
                    it.destinationAddress == "17.0.0.0" && it.destinationSubnetMask == "255.0.0.0"
                }) {
                    ipv4ExcludeRoutes.append(NEIPv4Route(destinationAddress: "17.0.0.0", subnetMask: "255.0.0.0"))
                }
            }

            ipv4Settings.includedRoutes = ipv4Routes
            ipv4Settings.excludedRoutes = ipv4ExcludeRoutes
            settings.ipv4Settings = ipv4Settings

            var ipv6Address: [String] = []
            var ipv6Prefixes: [NSNumber] = []
            let ipv6AddressIterator = options.getInet6Address()!
            while ipv6AddressIterator.hasNext() {
                let ipv6Prefix = ipv6AddressIterator.next()!
                ipv6Address.append(ipv6Prefix.address())
                ipv6Prefixes.append(NSNumber(value: ipv6Prefix.prefix()))
            }
            let ipv6Settings = NEIPv6Settings(addresses: ipv6Address, networkPrefixLengths: ipv6Prefixes)
            var ipv6Routes: [NEIPv6Route] = []
            var ipv6ExcludeRoutes: [NEIPv6Route] = []

            let inet6RouteAddressIterator = options.getInet6RouteAddress()!
            if inet6RouteAddressIterator.hasNext() {
                while inet6RouteAddressIterator.hasNext() {
                    let ipv6RoutePrefix = inet6RouteAddressIterator.next()!
                    ipv6Routes.append(NEIPv6Route(destinationAddress: ipv6RoutePrefix.address(), networkPrefixLength: NSNumber(value: ipv6RoutePrefix.prefix())))
                }
            } else if autoRouteUseSubRangesByDefault {
                ipv6Routes.append(NEIPv6Route(destinationAddress: "100::", networkPrefixLength: 8))
                ipv6Routes.append(NEIPv6Route(destinationAddress: "200::", networkPrefixLength: 7))
                ipv6Routes.append(NEIPv6Route(destinationAddress: "400::", networkPrefixLength: 6))
                ipv6Routes.append(NEIPv6Route(destinationAddress: "800::", networkPrefixLength: 5))
                ipv6Routes.append(NEIPv6Route(destinationAddress: "1000::", networkPrefixLength: 4))
                ipv6Routes.append(NEIPv6Route(destinationAddress: "2000::", networkPrefixLength: 3))
                ipv6Routes.append(NEIPv6Route(destinationAddress: "4000::", networkPrefixLength: 2))
                ipv6Routes.append(NEIPv6Route(destinationAddress: "8000::", networkPrefixLength: 1))
            } else {
                ipv6Routes.append(NEIPv6Route.default())
            }

            let inet6RouteExcludeAddressIterator = options.getInet6RouteExcludeAddress()!
            while inet6RouteExcludeAddressIterator.hasNext() {
                let ipv6RoutePrefix = inet6RouteExcludeAddressIterator.next()!
                ipv6ExcludeRoutes.append(NEIPv6Route(destinationAddress: ipv6RoutePrefix.address(), networkPrefixLength: NSNumber(value: ipv6RoutePrefix.prefix())))
            }

            if excludeDefaultRoute, !ipv6Routes.isEmpty {
                if !ipv6ExcludeRoutes.contains(where: { it in
                    it.destinationAddress == "::" && it.destinationNetworkPrefixLength == 127
                }) {
                    ipv6ExcludeRoutes.append(NEIPv6Route(destinationAddress: "::", networkPrefixLength: 127))
                }
            }

            ipv6Settings.includedRoutes = ipv6Routes
            ipv6Settings.excludedRoutes = ipv6ExcludeRoutes
            settings.ipv6Settings = ipv6Settings

            let hasDefaultRoute = ipv4Routes.contains(where: {
                $0.destinationAddress == "0.0.0.0" && $0.destinationSubnetMask == "0.0.0.0"
            })
            if !hasDefaultRoute {
                dnsSettings?.matchDomains = [""]
                dnsSettings?.matchDomainsNoSearch = true
            }
        }

        if options.isHTTPProxyEnabled() {
            let proxySettings = NEProxySettings()
            let proxyServer = NEProxyServer(address: options.getHTTPProxyServer(), port: Int(options.getHTTPProxyServerPort()))
            proxySettings.httpServer = proxyServer
            proxySettings.httpsServer = proxyServer
            if systemProxyEnabled {
                proxySettings.httpEnabled = true
                proxySettings.httpsEnabled = true
            }
            var bypassDomains: [String] = []
            let bypassDomainIterator = options.getHTTPProxyBypassDomain()!
            while bypassDomainIterator.hasNext() {
                bypassDomains.append(bypassDomainIterator.next())
            }
            if excludeAPNs, !bypassDomains.contains(where: { $0 == "push.apple.com" }) {
                bypassDomains.append("push.apple.com")
            }
            if !bypassDomains.isEmpty {
                proxySettings.exceptionList = bypassDomains
            }
            var matchDomains: [String] = []
            let matchDomainIterator = options.getHTTPProxyMatchDomain()!
            while matchDomainIterator.hasNext() {
                matchDomains.append(matchDomainIterator.next())
            }
            if !matchDomains.isEmpty {
                proxySettings.matchDomains = matchDomains
            }
            settings.proxySettings = proxySettings
        }

        networkSettings = settings
        try await tunnel.setTunnelNetworkSettings(settings)

        if let tunFd = tunnel.packetFlow.value(forKeyPath: "socket.fileDescriptor") as? Int32 {
            ret0_.pointee = tunFd
            return
        }
        let tunFdFromLoop = LibboxGetTunnelFileDescriptor()
        if tunFdFromLoop != -1 {
            ret0_.pointee = tunFdFromLoop
        } else {
            throw NSError(domain: "ExtensionPlatformInterface", code: 0, userInfo: [NSLocalizedDescriptionKey: "Missing file descriptor"])
        }
    }

    // MARK: Interface control / monitoring

    func usePlatformAutoDetectControl() -> Bool {
        false
    }

    func autoDetectControl(_: Int32) throws {}

    private var nwMonitor: NWPathMonitor?
    private var lastNetworkPath: String?

    func startDefaultInterfaceMonitor(_ listener: LibboxInterfaceUpdateListenerProtocol?) throws {
        guard let listener else {
            return
        }
        let monitor = NWPathMonitor()
        nwMonitor = monitor
        let semaphore = DispatchSemaphore(value: 0)
        monitor.pathUpdateHandler = { path in
            self.onUpdateDefaultInterface(listener, path)
            semaphore.signal()
            monitor.pathUpdateHandler = { path in
                self.onUpdateDefaultInterface(listener, path)
            }
        }
        monitor.start(queue: DispatchQueue.global())
        semaphore.wait()
    }

    private func onUpdateDefaultInterface(_ listener: LibboxInterfaceUpdateListenerProtocol, _ path: Network.NWPath) {
        let networkPath = describeNetworkPath(path)
        listener.updateNetworkPath(networkPath)
        if networkPath == lastNetworkPath {
            return
        }
        lastNetworkPath = networkPath
        guard path.status != .unsatisfied,
              let defaultInterface = path.availableInterfaces.first
        else {
            listener.updateDefaultInterface("", interfaceIndex: -1, isExpensive: false, isConstrained: false)
            return
        }
        listener.updateDefaultInterface(defaultInterface.name, interfaceIndex: Int32(defaultInterface.index), isExpensive: path.isExpensive, isConstrained: path.isConstrained)
    }

    private func describeNetworkPath(_ path: Network.NWPath) -> String {
        var components: [String] = []
        switch path.status {
        case .satisfied:
            components.append("satisfied")
        case .unsatisfied:
            components.append("unsatisfied(\(path.unsatisfiedReason))")
        case .requiresConnection:
            components.append("requiresConnection")
        @unknown default:
            components.append("unknown")
        }
        if !path.availableInterfaces.isEmpty {
            components.append("interfaces=" + path.availableInterfaces.map { "\($0.name)#\($0.index)/\($0.type)" }.joined(separator: ","))
        }
        if !path.gateways.isEmpty {
            components.append("gateways=" + path.gateways.map { "\($0)" }.sorted().joined(separator: ","))
        }
        if path.supportsIPv4 {
            components.append("ipv4")
        }
        if path.supportsIPv6 {
            components.append("ipv6")
        }
        if path.supportsDNS {
            components.append("dns")
        }
        if path.isExpensive {
            components.append("expensive")
        }
        if path.isConstrained {
            components.append("constrained")
        }
        return components.joined(separator: " ")
    }

    func closeDefaultInterfaceMonitor(_: LibboxInterfaceUpdateListenerProtocol?) throws {
        nwMonitor?.cancel()
        nwMonitor = nil
        lastNetworkPath = nil
    }

    func getInterfaces() throws -> any LibboxNetworkInterfaceIteratorProtocol {
        guard let nwMonitor else {
            throw NSError(domain: "ExtensionPlatformInterface", code: 0, userInfo: [NSLocalizedDescriptionKey: "NWMonitor not started"])
        }
        let path = nwMonitor.currentPath
        if path.status == .unsatisfied {
            return networkInterfaceArray([])
        }
        var interfaces: [LibboxNetworkInterface] = []
        for it in path.availableInterfaces {
            let interface = LibboxNetworkInterface()
            interface.name = it.name
            interface.index = Int32(it.index)
            switch it.type {
            case .wifi:
                interface.type = LibboxInterfaceTypeWIFI
            case .cellular:
                interface.type = LibboxInterfaceTypeCellular
            case .wiredEthernet:
                interface.type = LibboxInterfaceTypeEthernet
            default:
                interface.type = LibboxInterfaceTypeOther
            }
            interfaces.append(interface)
        }
        return networkInterfaceArray(interfaces)
    }

    class networkInterfaceArray: NSObject, LibboxNetworkInterfaceIteratorProtocol {
        private var iterator: IndexingIterator<[LibboxNetworkInterface]>
        init(_ array: [LibboxNetworkInterface]) {
            iterator = array.makeIterator()
        }

        private var nextValue: LibboxNetworkInterface?

        func hasNext() -> Bool {
            nextValue = iterator.next()
            return nextValue != nil
        }

        func next() -> LibboxNetworkInterface? {
            nextValue
        }
    }

    func underNetworkExtension() -> Bool {
        true
    }

    func includeAllNetworks() -> Bool {
        tunnel.overridePreferences?.includeAllNetworks ?? false
    }

    func clearDNSCache() {
        guard let networkSettings else {
            return
        }
        runBlocking {
            self.tunnel.reasserting = true
            defer { self.tunnel.reasserting = false }
            await withCheckedContinuation { continuation in
                self.tunnel.setTunnelNetworkSettings(nil) { _ in
                    continuation.resume()
                }
            }
            await withCheckedContinuation { continuation in
                self.tunnel.setTunnelNetworkSettings(networkSettings) { _ in
                    continuation.resume()
                }
            }
        }
    }

    func readWIFIState() -> LibboxWIFIState? {
        let network = runBlocking {
            await NEHotspotNetwork.fetchCurrent()
        }
        guard let network else {
            return nil
        }
        // gomobile names the second parameter from the Go signature
        // (NewWIFIState(wifiSSID, wifiBSSID)) — here imported unlabeled.
        return LibboxNewWIFIState(network.ssid, network.bssid)
    }

    // MARK: Tailscale device naming

    /// The built-in Tailscale endpoint uses this as the node hostname on the
    /// tailnet (upstream: DeviceKit's Device.current.safeDescription; here a
    /// dependency-free machine identifier).
    func tailscaleHostname() -> String {
        var systemInfo = utsname()
        uname(&systemInfo)
        let mirror = Mirror(reflecting: systemInfo.machine)
        let identifier = mirror.children.reduce(into: "") { result, element in
            guard let value = element.value as? Int8, value != 0 else { return }
            result.append(Character(UnicodeScalar(UInt8(value))))
        }
        return identifier.isEmpty ? "iPhone" : identifier
    }

    // MARK: Explicitly unsupported surfaces on iOS (same wording as upstream)

    func useProcFS() -> Bool {
        false
    }

    func findConnectionOwner(_: Int32, sourceAddress _: String?, sourcePort _: Int32, destinationAddress _: String?, destinationPort _: Int32) throws -> LibboxConnectionOwner {
        throw NSError(domain: "ExtensionPlatformInterface", code: 0, userInfo: [NSLocalizedDescriptionKey: "Not implemented"])
    }

    func startNeighborMonitor(_: LibboxNeighborUpdateListenerProtocol?) throws {}

    func closeNeighborMonitor(_: LibboxNeighborUpdateListenerProtocol?) throws {}

    func registerMyInterface(_ name: String?) {
        // Only meaningful with the macOS system extension; no-op on iOS
        // exactly like the upstream default branch.
        _ = name
    }

    func localDNSTransport() -> LibboxLocalDNSTransportProtocol? {
        nil
    }

    func connectSSHAgent(_ ret0_: UnsafeMutablePointer<Int32>?) throws {
        throw NSError(domain: "ExtensionPlatformInterface", code: -1, userInfo: [
            NSLocalizedDescriptionKey: "SSH agent forwarding is not supported",
        ])
    }

    func usePlatformShell() -> Bool {
        false
    }

    func checkPlatformShell() throws {
        throw NSError(domain: "ExtensionPlatformInterface", code: -1, userInfo: [
            NSLocalizedDescriptionKey: "SSH server is not supported",
        ])
    }

    func openShellSession(_: LibboxPlatformUser?, command _: String?, environ _: LibboxStringIteratorProtocol?, term _: String?, rows _: Int32, cols _: Int32) throws -> any LibboxShellSessionProtocol {
        throw NSError(domain: "ExtensionPlatformInterface", code: -1, userInfo: [
            NSLocalizedDescriptionKey: "SSH server is not supported",
        ])
    }

    func readSystemSSHHostKey(_ error: NSErrorPointer) -> String {
        error?.pointee = NSError(domain: "ExtensionPlatformInterface", code: -1, userInfo: [
            NSLocalizedDescriptionKey: "not supported on this platform",
        ])
        return ""
    }

    func lookupSFTPServer(_ error: NSErrorPointer) -> String {
        error?.pointee = NSError(domain: "ExtensionPlatformInterface", code: -1, userInfo: [
            NSLocalizedDescriptionKey: "lookupSFTPServer is not supported on Apple platforms",
        ])
        return ""
    }

    func lookupUser(_ username: String?) throws -> LibboxPlatformUser {
        throw NSError(domain: "ExtensionPlatformInterface", code: -1, userInfo: [
            NSLocalizedDescriptionKey: "SSH server is not supported",
        ])
    }

    func usePlatformBridge() -> Bool {
        false
    }

    func createBridge(_ options: LibboxBridgeOptions?) throws -> any LibboxBridgeSessionProtocol {
        throw NSError(domain: "ExtensionPlatformInterface", code: -1, userInfo: [
            NSLocalizedDescriptionKey: "bridge is not supported on this platform",
        ])
    }

    // MARK: CommandServerHandler (LibboxCommandServerHandlerProtocol)

    func serviceStop() throws {
        tunnel.stopTunnelService()
    }

    func serviceReload() throws {
        try runBlocking { [self] in
            try await tunnel.reloadTunnelService()
        }
    }

    func getSystemProxyStatus() throws -> LibboxSystemProxyStatus {
        let status = LibboxSystemProxyStatus()
        guard let networkSettings, let proxySettings = networkSettings.proxySettings,
              proxySettings.httpServer != nil
        else {
            return status
        }
        status.available = true
        status.enabled = proxySettings.httpEnabled
        return status
    }

    func setSystemProxyEnabled(_ enabled: Bool) throws {
        guard let networkSettings, let proxySettings = networkSettings.proxySettings,
              proxySettings.httpServer != nil, proxySettings.httpEnabled != enabled
        else {
            return
        }
        proxySettings.httpEnabled = enabled
        proxySettings.httpsEnabled = enabled
        networkSettings.proxySettings = proxySettings
        try runBlocking {
            try await self.tunnel.setTunnelNetworkSettings(networkSettings)
        }
    }

    func triggerNativeCrash() throws {
        DispatchQueue.global().asyncAfter(deadline: .now() + .milliseconds(200)) {
            fatalError("debug native crash")
        }
    }

    func writeDebugMessage(_ message: String?) {
        guard let message else {
            return
        }
        tunnel.writeTunnelMessage(message)
    }

    // MARK: Notifications

    func send(_ notification: LibboxNotification?) throws {
        guard let notification else {
            return
        }
        let center = UNUserNotificationCenter.current()
        let content = UNMutableNotificationContent()
        content.title = notification.title
        content.subtitle = notification.subtitle
        content.body = notification.body
        if !notification.openURL.isEmpty {
            content.userInfo["OPEN_URL"] = notification.openURL
            content.categoryIdentifier = "OPEN_URL"
        }
        content.interruptionLevel = .active
        let request = UNNotificationRequest(identifier: notification.identifier, content: content, trigger: nil)
        try runBlocking {
            try await center.requestAuthorization(options: [.alert])
            try await center.add(request)
        }
    }

    func cancelNotification(_ identifier: String?, typeID _: Int32) throws {
        guard let identifier else {
            return
        }
        let center = UNUserNotificationCenter.current()
        center.removePendingNotificationRequests(withIdentifiers: [identifier])
        center.removeDeliveredNotifications(withIdentifiers: [identifier])
    }

    // MARK: Reset

    func reset() {
        networkSettings = nil
        nwMonitor?.cancel()
        nwMonitor = nil
        lastNetworkPath = nil
    }
}
