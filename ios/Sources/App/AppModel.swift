import Foundation
import Observation
import WidgetKit

@MainActor
@Observable
final class AppModel {
    var snapshot: Snapshot?
    var health: Health?
    var settings: ServerSettings = ServerSettings()
    var preferences: DisplayPreferences = DisplayPreferences()
    var isConfigured: Bool = false
    var isLoading: Bool = false
    var isTestingAction: Bool = false
    var errorMessage: String?
    var statusMessage: String?
    var notificationStatus: String = "unknown"

    private let keychain: KeychainStore
    private let store: SnapshotStore
    private(set) var credentials: ConnectionCredentials?

    init(keychain: KeychainStore = .shared, store: SnapshotStore = .shared) {
        self.keychain = keychain
        self.store = store
        self.preferences = store.loadPreferences()
        self.snapshot = store.loadSnapshot()
        if let creds = try? keychain.load() {
            self.credentials = creds
            self.isConfigured = true
            store.mirrorCredentials(creds)
        } else if let mirrored = store.mirroredCredentials() {
            self.credentials = mirrored
            self.isConfigured = true
        }
    }

    var visibleProviders: [Provider] {
        guard let snapshot else { return [] }
        return ProviderDisplay.orderedVisible(
            providers: snapshot.providers,
            order: preferences.providerOrder,
            hidden: preferences.hiddenSet
        )
    }

    var dataAgeText: String {
        guard let fetched = snapshot?.fetchedAt else { return "No data yet" }
        return "Updated \(RelativeTime.string(for: fetched))"
    }

    func client() throws -> APIClient {
        guard let credentials else { throw APIError.invalidBaseURL }
        return try APIClient.make(credentials: credentials)
    }

    func saveConnection(url: String, token: String) async throws {
        let creds = ConnectionCredentials(serverURL: url, token: token)
        let client = try APIClient.make(credentials: creds)
        let health = try await client.fetchHealth()
        try keychain.save(creds)
        store.mirrorCredentials(creds)
        self.credentials = creds
        self.isConfigured = true
        self.health = health
        self.errorMessage = nil
        await refresh()
        await registerTokensIfNeeded()
    }

    func refresh() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let client = try client()
            async let snapTask = client.fetchSnapshot()
            async let healthTask = client.fetchHealth()
            async let settingsTask = client.fetchSettings()
            let (snap, health, settings) = try await (snapTask, healthTask, settingsTask)
            self.snapshot = snap
            self.health = health
            self.settings = settings
            self.preferences = DisplayPreferences(
                providerOrder: settings.providerOrder,
                hiddenProviders: settings.hiddenProviders
            )
            try store.saveSnapshot(snap)
            try store.savePreferences(preferences)
            errorMessage = nil
            WidgetCenter.shared.reloadAllTimelines()
        } catch {
            if let cached = store.loadSnapshot() {
                snapshot = cached
            }
            errorMessage = String(describing: error)
        }
    }

    func applySettings() async {
        do {
            let client = try client()
            var next = settings
            next.providerOrder = preferences.providerOrder
            next.hiddenProviders = preferences.hiddenProviders
            let updated = try await client.updateSettings(next)
            settings = updated
            preferences = DisplayPreferences(
                providerOrder: updated.providerOrder,
                hiddenProviders: updated.hiddenProviders
            )
            try store.savePreferences(preferences)
            WidgetCenter.shared.reloadAllTimelines()
            errorMessage = nil
        } catch {
            errorMessage = String(describing: error)
        }
    }

    func moveProvider(from source: IndexSet, to destination: Int) {
        var order = preferences.providerOrder
        // Ensure all known providers are in order list
        if let providers = snapshot?.providers {
            for p in providers where !order.contains(p.id) {
                order.append(p.id)
            }
        }
        order.move(fromOffsets: source, toOffset: destination)
        preferences.providerOrder = order
        Task { await applySettings() }
    }

    func setHidden(_ id: String, hidden: Bool) {
        var hiddenList = preferences.hiddenProviders
        if hidden {
            if !hiddenList.contains(id) { hiddenList.append(id) }
        } else {
            hiddenList.removeAll { $0 == id }
        }
        preferences.hiddenProviders = hiddenList
        Task { await applySettings() }
    }

    func registerTokensIfNeeded(apnsToken: String? = nil, widgetToken: String? = nil) async {
        do {
            let client = try client()
            let deviceID = store.deviceID()
            let widget = widgetToken ?? store.pendingWidgetToken()
            let reg = DeviceRegistration(deviceID: deviceID, apnsToken: apnsToken, widgetToken: widget)
            _ = try await client.registerDevice(reg)
            errorMessage = nil
        } catch {
            // Soft-fail — connection may not be configured yet.
            if isConfigured {
                errorMessage = "Device registration failed: \(error)"
            }
        }
    }

    func forcePoll() async {
        isTestingAction = true
        defer { isTestingAction = false }
        do {
            let client = try client()
            let result = try await client.forcePoll()
            if result.success {
                statusMessage = "Polled — \(result.events) event(s)" +
                    (result.snapshotChanged ? ", snapshot changed" : "")
                errorMessage = nil
            } else {
                errorMessage = result.error ?? "Poll failed"
                statusMessage = nil
            }
            await refresh()
        } catch {
            errorMessage = String(describing: error)
            statusMessage = nil
        }
    }

    func sendDemoAlert() async {
        isTestingAction = true
        defer { isTestingAction = false }
        do {
            let client = try client()
            let result = try await client.sendDemoAlert()
            statusMessage = "Test alert → \(result.devicesAlerted) device(s), \(result.widgetsRefreshed) widget(s)"
            errorMessage = nil
        } catch {
            errorMessage = String(describing: error)
            statusMessage = nil
        }
    }
}


