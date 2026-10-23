import Foundation
import Security

public enum KeychainError: Error, Equatable {
    case unexpectedStatus(OSStatus)
    case missingValue
}

public struct ConnectionCredentials: Codable, Equatable, Sendable {
    public var serverURL: String
    public var token: String

    public init(serverURL: String, token: String) {
        self.serverURL = serverURL
        self.token = token
    }
}

/// The credential operations AppModel needs; lets tests run without Keychain entitlements.
public protocol CredentialStoring: AnyObject {
    func save(_ credentials: ConnectionCredentials) throws
    func load() throws -> ConnectionCredentials?
}

public final class KeychainStore: CredentialStoring, @unchecked Sendable {
    public static let shared = KeychainStore(accessGroup: AppConstants.keychainAccessGroup)

    private let service: String
    private let accessGroup: String?

    public init(service: String = AppConstants.keychainService, accessGroup: String? = AppConstants.keychainAccessGroup) {
        self.service = service
        self.accessGroup = accessGroup
    }

    private enum Account: String {
        case credentials = "connectionCredentials"
        case serverURL = "serverURL"
        case token = "bearerToken"
    }

    public func save(_ credentials: ConnectionCredentials) throws {
        let data = try JSONEncoder().encode(credentials)
        let query = baseQuery(account: .credentials) as CFDictionary
        let attributes = [kSecValueData as String: data] as CFDictionary
        var status = SecItemUpdate(query, attributes)
        if status == errSecItemNotFound {
            status = add(data)
            // Another process may have created the item since the update.
            if status == errSecDuplicateItem {
                status = SecItemUpdate(query, attributes)
            }
        }
        guard status == errSecSuccess else {
            throw KeychainError.unexpectedStatus(status)
        }
        try clearLegacyItems()
    }

    public func load() throws -> ConnectionCredentials? {
        if let data = try get(account: .credentials) {
            return try JSONDecoder().decode(ConnectionCredentials.self, from: data)
        }

        // Legacy items are read-only now. Publish their pair only if no app or
        // widget has already saved/migrated credentials; never overwrite a winner.
        guard let urlData = try get(account: .serverURL),
              let tokenData = try get(account: .token),
              let url = String(data: urlData, encoding: .utf8),
              let token = String(data: tokenData, encoding: .utf8) else {
            // A concurrent save may have removed the legacy items.
            return try loadPair()
        }
        let data = try JSONEncoder().encode(ConnectionCredentials(serverURL: url, token: token))
        let status = add(data)
        guard status == errSecSuccess || status == errSecDuplicateItem else {
            throw KeychainError.unexpectedStatus(status)
        }
        try clearLegacyItems()
        return try loadPair()
    }

    public func clear() throws {
        try clearLegacyItems()
        try delete(account: .credentials)
    }

    private func loadPair() throws -> ConnectionCredentials? {
        guard let data = try get(account: .credentials) else { return nil }
        return try JSONDecoder().decode(ConnectionCredentials.self, from: data)
    }

    private func clearLegacyItems() throws {
        try delete(account: .serverURL)
        try delete(account: .token)
    }

    private func add(_ data: Data) -> OSStatus {
        var query = baseQuery(account: .credentials)
        query[kSecValueData as String] = data
        query[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        query[kSecAttrSynchronizable as String] = false
        return SecItemAdd(query as CFDictionary, nil)
    }

    private func get(account: Account) throws -> Data? {
        var query = baseQuery(account: account)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecItemNotFound {
            return nil
        }
        guard status == errSecSuccess, let data = item as? Data else {
            throw KeychainError.unexpectedStatus(status)
        }
        return data
    }

    private func delete(account: Account) throws {
        let status = SecItemDelete(baseQuery(account: account) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError.unexpectedStatus(status)
        }
    }

    private func baseQuery(account: Account) -> [String: Any] {
        var q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account.rawValue,
        ]
        if let accessGroup {
            q[kSecAttrAccessGroup as String] = accessGroup
        }
        return q
    }
}
