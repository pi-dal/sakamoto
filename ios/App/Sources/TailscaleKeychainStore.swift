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
    private let service: String
    private let account: String

    /// - Parameters:
    ///   - service: defaults to the app bundle identifier; both the app and
    ///     the tunnel must use the same value to share the item.
    ///   - account: stable key name ("tailscale-auth-key").
    init(service: String = Bundle.main.bundleIdentifier ?? "com.pidal.sakamoto",
         account: String = "tailscale-auth-key") {
        self.service = service
        self.account = account
    }

    var hasAuthKey: Bool {
        (readAuthKey() ?? "").isEmpty == false
    }

    func readAuthKey() -> String? {
        var query = baseQuery()
        query[kSecReturnData as String] = kCFBooleanTrue
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        guard status == errSecSuccess, let data = item as? Data else {
            return nil
        }
        return String(data: data, encoding: .utf8)
    }

    func storeAuthKey(_ key: String) throws {
        guard let data = key.data(using: .utf8), !key.isEmpty else {
            return
        }
        // Upsert: delete then add keeps this simple and side-effect free
        // (no update-on-missing-error dance). The key is operator input via
        // a secure text field; it lands here and nowhere else.
        try? deleteAuthKey()

        var query = baseQuery()
        query[kSecValueData as String] = data
        query[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        let status = SecItemAdd(query as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw AppSetupError("keychain store failed (status \(status))")
        }
    }

    func deleteAuthKey() throws {
        let status = SecItemDelete(baseQuery() as CFDictionary)
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
