import Foundation
import Security

/// A device-only authority shared by the app and its own tunnel extension.
/// Private key material never enters preferences, IPC, logs or export files.
public enum LocationAuthorityStore {
    private static let service = "com.pidal.sakamoto.location.authority"
    private static let account = "device-ca-v1"
    private static func query() -> [String: Any] {
        // Apple treats application groups as Keychain access groups directly;
        // unlike keychain-access-groups, these names have no Team ID prefix.
        let group = SystemSurfaceStore.groupIdentifier
        return [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account, kSecAttrAccessGroup as String: group]
    }
    public static func load() throws -> String? {
        var query = query(); query[kSecReturnData as String] = true; query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data, let raw = String(data: data, encoding: .utf8) else {
            throw TunnelProfile.InvalidProfile("Location certificate is unavailable (Keychain \(status))")
        }
        return raw
    }
    public static func save(_ raw: String) throws {
        let query = query()
        let data = Data(raw.utf8)
        var status = SecItemUpdate(query as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if status == errSecItemNotFound {
            var attributes = query
            attributes[kSecValueData as String] = data
            attributes[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
            status = SecItemAdd(attributes as CFDictionary, nil)
        }
        guard status == errSecSuccess else { throw TunnelProfile.InvalidProfile("Could not store location certificate (Keychain \(status))") }
    }
    public static func fingerprint(_ raw: String) -> String {
        guard let data = raw.data(using: .utf8), let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return "" }
        return object["fingerprint"] as? String ?? ""
    }
    public static func markTrusted(_ fingerprint: String) { VirtualLocationStore.defaults?.set(fingerprint, forKey: "sakamoto.location.trustedCA") }
    public static func wasTrusted(_ raw: String) -> Bool {
        let fingerprint = fingerprint(raw)
        return !fingerprint.isEmpty && VirtualLocationStore.defaults?.string(forKey: "sakamoto.location.trustedCA") == fingerprint
    }
}
