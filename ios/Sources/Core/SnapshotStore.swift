import Foundation

public struct DisplayPreferences: Codable, Equatable, Sendable {
    public var providerOrder: [String]
    public var hiddenProviders: [String]

    public init(providerOrder: [String] = ["codex", "claude", "grok"], hiddenProviders: [String] = []) {
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
        static let lastRefresh = "lastRefresh"
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

    public func saveSnapshot(_ snapshot: Snapshot) throws {
        let data = try JSONCoding.encoder.encode(snapshot)
        defaults.set(data, forKey: Key.snapshot)
        defaults.set(snapshot.fetchedAt, forKey: Key.lastRefresh)
    }

    public func loadSnapshot() -> Snapshot? {
        guard let data = defaults.data(forKey: Key.snapshot) else { return nil }
        return try? JSONCoding.decoder.decode(Snapshot.self, from: data)
    }

    public var lastRefresh: Date? {
        defaults.object(forKey: Key.lastRefresh) as? Date
    }

    public func savePreferences(_ prefs: DisplayPreferences) throws {
        let data = try JSONCoding.encoder.encode(prefs)
        defaults.set(data, forKey: Key.prefs)
    }

    public func loadPreferences() -> DisplayPreferences {
        guard let data = defaults.data(forKey: Key.prefs),
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
