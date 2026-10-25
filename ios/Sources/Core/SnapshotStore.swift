import Foundation
import CryptoKit

public struct DisplayPreferences: Codable, Equatable, Sendable {
    public var providerOrder: [String]
    public var hiddenProviders: [String]

    public init(providerOrder: [String] = ProviderCatalog.defaultOrder, hiddenProviders: [String] = []) {
        self.providerOrder = providerOrder
        self.hiddenProviders = hiddenProviders
    }

    public var hiddenSet: Set<String> { Set(hiddenProviders) }
}

public final class SnapshotStore: @unchecked Sendable {
    public static let shared = SnapshotStore()

    private let defaults: UserDefaults
    private let suiteName: String?

    private enum Key {
        static let snapshot = "snapshot.json"
        static let prefs = "displayPreferences.json"
        static let deviceID = "deviceID"
        static let pendingWidgetToken = "pendingWidgetToken"
        static let mirroredURL = "mirroredServerURL"
        static let mirroredToken = "mirroredBearerToken"
    }

    public init(suiteName: String? = AppConstants.appGroupID) {
        self.suiteName = suiteName
        if let suiteName, let ud = UserDefaults(suiteName: suiteName) {
            self.defaults = ud
        } else {
            self.defaults = UserDefaults.standard
        }
    }

    /// Test helper — isolated suite.
    public static func temporary() -> SnapshotStore {
        let name = "usagewidget.tests.\(UUID().uuidString)"
        return SnapshotStore(suiteName: name)
    }

    // Hash the complete pair: token rotation on the same URL is a new connection.
    // Unscoped legacy values cannot be attributed safely and are never read.
    private func cacheKey(_ key: String, connection: ConnectionCredentials?) -> String {
        guard let connection else { return key + ".unconfigured" }
        let url = Data(connection.serverURL.utf8)
        var data = Data("\(url.count):".utf8)
        data.append(url)
        data.append(Data(connection.token.utf8))
        let digest = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
        return key + "." + digest
    }

    public func saveSnapshot(_ snapshot: Snapshot, connection: ConnectionCredentials? = nil) throws {
        let data = try JSONCoding.encoder.encode(snapshot)
        defaults.set(data, forKey: cacheKey(Key.snapshot, connection: connection))
    }

    public func loadSnapshot(connection: ConnectionCredentials? = nil) -> Snapshot? {
        guard let data = defaults.data(forKey: cacheKey(Key.snapshot, connection: connection)) else { return nil }
        return try? JSONCoding.decoder.decode(Snapshot.self, from: data)
    }

    public func savePreferences(_ prefs: DisplayPreferences, connection: ConnectionCredentials? = nil) throws {
        let data = try JSONCoding.encoder.encode(prefs)
        defaults.set(data, forKey: cacheKey(Key.prefs, connection: connection))
    }

    public func loadPreferences(connection: ConnectionCredentials? = nil) -> DisplayPreferences {
        guard let data = defaults.data(forKey: cacheKey(Key.prefs, connection: connection)),
              let prefs = try? JSONCoding.decoder.decode(DisplayPreferences.self, from: data) else {
            return DisplayPreferences()
        }
        return prefs
    }

    public func deviceID() -> String {
        if let existing = defaults.string(forKey: Key.deviceID), !existing.isEmpty {
            return existing
        }
        let id = UUID().uuidString.lowercased()
        defaults.set(id, forKey: Key.deviceID)
        return id
    }

    public func setPendingWidgetToken(_ hex: String?) {
        defaults.set(hex, forKey: Key.pendingWidgetToken)
    }

    public func pendingWidgetToken() -> String? {
        defaults.string(forKey: Key.pendingWidgetToken)
    }

    /// Mirror credentials into the App Group so the widget can call the API without a shared keychain group.
    public func mirrorCredentials(_ credentials: ConnectionCredentials?) {
        if let credentials {
            defaults.set(credentials.serverURL, forKey: Key.mirroredURL)
            defaults.set(credentials.token, forKey: Key.mirroredToken)
        } else {
            defaults.removeObject(forKey: Key.mirroredURL)
            defaults.removeObject(forKey: Key.mirroredToken)
        }
    }

    public func mirroredCredentials() -> ConnectionCredentials? {
        guard let url = defaults.string(forKey: Key.mirroredURL),
              let token = defaults.string(forKey: Key.mirroredToken),
              !url.isEmpty, !token.isEmpty else {
            return nil
        }
        return ConnectionCredentials(serverURL: url, token: token)
    }
}
