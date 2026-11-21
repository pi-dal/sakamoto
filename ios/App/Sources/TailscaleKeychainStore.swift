import Foundation
import Security
import SakamotoKit

// Keychain-backed auth-key storage for the built-in Tailscale endpoint.
//
// Contract (TailscaleAuthKeyStoring):
//   - the key never appears in logs, notices, or debug descriptions;
//   - accessibility is after-first-unlock: the tunnel provider (which starts
//     under lock on demand) can read it, other apps cannot;
//   - the app-group is NOT used for the secret itself (app-group files are
//     plain files); only the non-secret endpoint tag lives in defaults.

final class TailscaleKeychainStore: TailscaleAuthKeyStoring {
    private let loadShared: @Sendable () throws -> String?
    private let saveShared: @Sendable (String) throws -> Void
    private let removeShared: @Sendable () throws -> Void
    private let removeLegacy: @Sendable (CFDictionary) -> OSStatus
    private let service: String
    private let account: String
    private let update: @Sendable (CFDictionary, CFDictionary) -> OSStatus

    /// - Parameters:
    ///   - service: defaults to the app bundle identifier; both the app and
    ///     the tunnel must use the same value to share the item.
    ///   - account: stable key name ("tailscale-auth-key").
    init(service: String = Bundle.main.bundleIdentifier ?? "com.pidal.sakamoto",
         account: String = "tailscale-auth-key",
         loadShared: @escaping @Sendable () throws -> String? = SharedTailscaleKey.load,
         saveShared: @escaping @Sendable (String) throws -> Void = SharedTailscaleKey.save,
         removeShared: @escaping @Sendable () throws -> Void = SharedTailscaleKey.delete,
         removeLegacy: @escaping @Sendable (CFDictionary) -> OSStatus = SecItemDelete,
         update: @escaping @Sendable (CFDictionary, CFDictionary) -> OSStatus = SecItemUpdate) {
        self.loadShared = loadShared
        self.saveShared = saveShared
        self.removeShared = removeShared
        self.removeLegacy = removeLegacy
        self.update = update
        self.service = service
        self.account = account
    }

    var hasAuthKey: Bool {
        ((try? readAuthKey()) ?? "").isEmpty == false
    }

    func readAuthKey() throws -> String? {
        if account == "tailscale-auth-key", let shared = try loadShared() {
            try deleteLegacyItem()
            return shared
        }
        var query = baseQuery()
        query[kSecReturnData as String] = kCFBooleanTrue
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = item as? Data, let key = String(data: data, encoding: .utf8), !key.isEmpty else {
            throw AppSetupError("Could not read the saved credential (status \(status))")
        }
        if account == "tailscale-auth-key" {
            try saveShared(key)
            try deleteLegacyItem()
        }
        return key
    }

    func storeAuthKey(_ key: String) throws {
        guard let data = key.data(using: .utf8), !key.isEmpty else {
            return
        }
        if account == "tailscale-auth-key" {
            // Finish migration of the currently usable key before committing its replacement.
            _ = try readAuthKey()
            try saveShared(key)
            return
        }
        let query = baseQuery()
        let attributes: [String: Any] = [kSecValueData as String: data,
                                         kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlock]
        var status = update(query as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            let item = query.merging(attributes) { _, new in new }
            status = SecItemAdd(item as CFDictionary, nil)
        }
        guard status == errSecSuccess else {
            throw AppSetupError("keychain store failed (status \(status))")
        }
    }

    func deleteAuthKey() throws {
        guard account == "tailscale-auth-key" else { try deleteLegacyItem(); return }
        // Delete the item supplying the current credential last. A failed first
        // step leaves the effective key available to the next connection.
        if try loadShared() == nil {
            try removeShared()
            try deleteLegacyItem()
        } else {
            try deleteLegacyItem()
            try removeShared()
        }
    }

    private func deleteLegacyItem() throws {
        let status = removeLegacy(baseQuery() as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw AppSetupError("keychain delete failed (status \(status))")
        }
    }

    private func baseQuery() -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }
}
