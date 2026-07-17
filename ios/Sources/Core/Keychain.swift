import Foundation
import Security

public enum KeychainError: Error, Equatable {
    case unexpectedStatus(OSStatus)
    case missingValue
}

public struct ConnectionCredentials: Equatable, Sendable {
    public var serverURL: String
    public var token: String

    public init(serverURL: String, token: String) {
        self.serverURL = serverURL
        self.token = token
    }
}

public final class KeychainStore: @unchecked Sendable {
    public static let shared = KeychainStore()

    private let service: String
    private let accessGroup: String?

    public init(service: String = AppConstants.keychainService, accessGroup: String? = nil) {
        self.service = service
        // Access group requires a real App ID prefix at runtime; omit for simulator/unsigned builds
        // and rely on app-group defaults mirroring for the widget when keychain group is unavailable.
        self.accessGroup = accessGroup
    }

    private enum Account: String {
        case serverURL = "serverURL"
        case token = "bearerToken"
    }

    public func save(_ credentials: ConnectionCredentials) throws {
        try set(credentials.serverURL, account: .serverURL)
        try set(credentials.token, account: .token)
    }

    public func load() throws -> ConnectionCredentials? {
        let url = try get(account: .serverURL)
        let token = try get(account: .token)
        switch (url, token) {
        case let (u?, t?):
            return ConnectionCredentials(serverURL: u, token: t)
        case (nil, nil):
            return nil
        default:
            return nil
        }
    }

    public func clear() throws {
        try delete(account: .serverURL)
        try delete(account: .token)
    }

    private func set(_ value: String, account: Account) throws {
        let data = Data(value.utf8)
        var query = baseQuery(account: account)
        SecItemDelete(query as CFDictionary)
        query[kSecValueData as String] = data
        query[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        let status = SecItemAdd(query as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw KeychainError.unexpectedStatus(status)
        }
    }

    private func get(account: Account) throws -> String? {
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
        return String(data: data, encoding: .utf8)
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
