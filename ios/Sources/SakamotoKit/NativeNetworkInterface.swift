import Darwin
import Foundation

/// Physical interface details for libbox's platform interface finder and
/// Tailscale's network monitor. NWPath provides identity/type, not IPs or flags.
public struct NativeNetworkInterface: Equatable {
    public var flags: UInt32 = 0
    public var mtu: UInt32 = 0
    public var addresses: [String] = []

    public static func read() throws -> [String: NativeNetworkInterface] {
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0 else {
            throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
        }
        defer { freeifaddrs(head) }
        var result: [String: NativeNetworkInterface] = [:]
        var cursor = head
        while let entry = cursor {
            defer { cursor = entry.pointee.ifa_next }
            let value = entry.pointee
            let name = String(cString: value.ifa_name)
            var interface = result[name] ?? NativeNetworkInterface()
            interface.flags = value.ifa_flags
            if let address = value.ifa_addr {
                if Int32(address.pointee.sa_family) == AF_LINK, let data = value.ifa_data {
                    interface.mtu = data.assumingMemoryBound(to: if_data.self).pointee.ifi_mtu
                }
                if let mask = value.ifa_netmask, let prefix = prefix(address: address, mask: mask) {
                    interface.addresses.append(prefix)
                }
            }
            result[name] = interface
        }
        for name in Array(result.keys) {
            let addresses = result[name]!.addresses
            result[name]?.addresses = Array(Set(addresses)).sorted()
        }
        return result
    }

    private static func prefix(address: UnsafePointer<sockaddr>, mask: UnsafePointer<sockaddr>) -> String? {
        let family = Int32(address.pointee.sa_family)
        let offset: Int
        let width: Int
        switch family {
        case AF_INET:
            offset = MemoryLayout<sockaddr_in>.offset(of: \.sin_addr)!
            width = MemoryLayout<in_addr>.size
        case AF_INET6:
            offset = MemoryLayout<sockaddr_in6>.offset(of: \.sin6_addr)!
            width = MemoryLayout<in6_addr>.size
        default:
            return nil
        }
        // Darwin permits shortened netmask sockaddrs; trailing mask bytes
        // are zero. Read only the bytes advertised by sa_len.
        let available = min(width, max(0, Int(mask.pointee.sa_len) - offset))
        guard available > 0 else { return nil }
        let start = UnsafeRawPointer(mask).advanced(by: offset).assumingMemoryBound(to: UInt8.self)
        let bytes = Array(UnsafeBufferPointer(start: start, count: available))
            + Array(repeating: UInt8(0), count: width - available)
        guard let length = prefixLength(bytes) else { return nil }
        var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
        guard getnameinfo(address, socklen_t(address.pointee.sa_len), &host,
                          socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 else { return nil }
        // Go's netip.ParsePrefix rejects scoped IPv6 literals. The interface
        // identity supplies the scope separately.
        let ip = String(cString: host).split(separator: "%", maxSplits: 1)[0]
        return "\(ip)/\(length)"
    }

    static func prefixLength(_ mask: [UInt8]) -> Int? {
        var length = 0
        var seenZero = false
        for byte in mask {
            for bit in (0..<8).reversed() {
                if byte & (1 << bit) == 0 {
                    seenZero = true
                } else {
                    guard !seenZero else { return nil }
                    length += 1
                }
            }
        }
        return length
    }
}
