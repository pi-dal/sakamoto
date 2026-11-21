import Foundation
import Security

/// Device-only item accessible to the signed app and its packet tunnel.
public enum SharedTailscaleKey {
    private static var query: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: "com.pidal.sakamoto.tailscale",
         kSecAttrAccount as String: "auth-key",
         kSecAttrAccessGroup as String: SystemSurfaceStore.groupIdentifier]
    }

    public static func load() throws -> String? {
        var q = query
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(q as CFDictionary, &item)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = item as? Data, let key = String(data: data, encoding: .utf8) else {
            throw TunnelProfile.InvalidProfile("Tailscale Keychain unavailable (\(status))")
        }
        return key
    }

    public static func save(_ key: String) throws {
        let attrs: [String: Any] = [kSecValueData as String: Data(key.utf8), kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly]
        var status = SecItemUpdate(query as CFDictionary, attrs as CFDictionary)
        if status == errSecItemNotFound { status = SecItemAdd(query.merging(attrs) { _, new in new } as CFDictionary, nil) }
        guard status == errSecSuccess else { throw TunnelProfile.InvalidProfile("Tailscale Keychain write failed (\(status))") }
    }

    public static func delete() throws {
        let status = SecItemDelete(query as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw TunnelProfile.InvalidProfile("Tailscale Keychain delete failed (\(status))") }
    }
}
