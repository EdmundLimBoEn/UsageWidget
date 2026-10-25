import Foundation
import Observation
import WidgetKit
import UserNotifications

@MainActor
@Observable
final class AppModel {
    var snapshot: Snapshot?
    var health: Health?
    var settings: ServerSettings = ServerSettings()
    var preferences: DisplayPreferences = DisplayPreferences()
    var isConfigured: Bool = false
    var isPreviewingSample: Bool = false
    var isLoading: Bool = false
    var isTestingAction: Bool = false
    var errorMessage: String?
    var statusMessage: String?
    var notificationStatus: String = "unknown"

    enum HomeSurface: Equatable {
        case setup
        case samplePreview
        case live
    }

    var homeSurface: HomeSurface {
        if isConfigured { return .live }
        if isPreviewingSample { return .samplePreview }
        return .setup
    }

    var notificationStatusLabel: String {
        switch notificationStatus {
        case "authorized": "On"
        case "denied": "Off"
        case "not determined": "Not asked"
        case "provisional": "Quiet delivery"
        case "ephemeral": "Temporary"
        case "registration failed": "Registration failed"
        default: notificationStatus
        }
    }

    var readiness: Readiness?

    var notificationsAuthorized: Bool {
        ["authorized", "provisional", "ephemeral"].contains(notificationStatus)
    }

    var offersNotificationPermission: Bool {
        DeliveryState.offersNotificationPermission(health: health)
    }

    var deliveryState: DeliveryState {
        DeliveryState.evaluate(health: health, readiness: readiness, notificationsAuthorized: notificationsAuthorized)
    }

    private let keychain: any CredentialStoring
    private let store: SnapshotStore
    private let session: URLSession
    private var connectionGeneration = UUID()
    private var connectionAttempt = UUID()
    private(set) var credentials: ConnectionCredentials?

    init(keychain: any CredentialStoring = KeychainStore.shared, store: SnapshotStore = .shared, session: URLSession = .shared) {
        self.keychain = keychain
        self.store = store
        self.session = session
        if let creds = try? keychain.load() {
            self.credentials = creds
            self.isConfigured = true
            store.mirrorCredentials(nil)
        } else if let mirrored = store.mirroredCredentials() {
            self.credentials = mirrored
            self.isConfigured = true
            if (try? keychain.save(mirrored)) != nil {
                store.mirrorCredentials(nil)
            }
        }
        self.preferences = store.loadPreferences(connection: credentials)
        self.snapshot = store.loadSnapshot(connection: credentials)
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

    var freshness: DataFreshness {
        if homeSurface == .samplePreview { return .sample }
        if isLoading { return .collecting }
        if snapshot == nil { return errorMessage == nil ? .collecting : .unavailable }
        if snapshot?.stale == true { return .stale }
        if let collector = health?.collector, collector.status == "down" || collector.status == "degraded" {
            return .stale
        }
        if let fetched = snapshot?.fetchedAt, Date().timeIntervalSince(fetched) > 10 * 60 {
            return .stale
        }
        return .current
    }

    func client() throws -> APIClient {
        guard let credentials else { throw APIError.invalidBaseURL }
        return try APIClient.make(credentials: credentials, session: session)
    }

    func saveConnection(url: String, token: String) async throws {
        let attempt = UUID()
        connectionAttempt = attempt
        let creds = ConnectionCredentials(serverURL: url, token: token)
        let client = try APIClient.make(credentials: creds, session: session)
        let health: Health
        do {
            health = try await client.fetchHealth()
        } catch {
            guard connectionAttempt == attempt else { return }
            throw error
        }
        guard connectionAttempt == attempt else { return }
        try keychain.save(creds)
        connectionGeneration = UUID()
        let generation = connectionGeneration
        store.mirrorCredentials(nil)
        self.credentials = creds
        self.snapshot = store.loadSnapshot(connection: creds)
        self.preferences = store.loadPreferences(connection: creds)
        self.settings = ServerSettings()
        self.readiness = nil
        self.statusMessage = nil
        self.isLoading = false
        self.isTestingAction = false
        WidgetCenter.shared.reloadAllTimelines()
        self.isConfigured = true
        self.isPreviewingSample = false
        self.health = health
        self.errorMessage = nil
        await refresh()
        guard connectionGeneration == generation else { return }
        await registerTokensIfNeeded()
    }

    func enterSamplePreview() {
        isPreviewingSample = true
        snapshot = SampleCapacity.snapshot
        errorMessage = nil
        statusMessage = nil
    }

    func exitSamplePreview() {
        guard !isConfigured else { return }
        isPreviewingSample = false
        snapshot = store.loadSnapshot(connection: credentials)
        errorMessage = nil
        statusMessage = nil
    }

    func refresh() async {
        let generation = connectionGeneration
        guard isConfigured else { return }
        isLoading = true
        defer { if connectionGeneration == generation { isLoading = false } }
        do {
            let client = try client()
            async let snapTask = client.fetchSnapshot()
            async let healthTask = client.fetchHealth()
            async let settingsTask = client.fetchSettings()
            let (snap, health, settings) = try await (snapTask, healthTask, settingsTask)
            guard connectionGeneration == generation else { return }
            self.snapshot = snap
            self.health = health
            self.settings = settings
            self.preferences = DisplayPreferences(
                providerOrder: settings.providerOrder,
                hiddenProviders: settings.hiddenProviders
            )
            try store.saveSnapshot(snap, connection: credentials)
            try store.savePreferences(preferences, connection: credentials)
            errorMessage = nil
            WidgetCenter.shared.reloadAllTimelines()
        } catch {
            guard connectionGeneration == generation else { return }
            if let cached = store.loadSnapshot(connection: credentials) {
                snapshot = cached
            }
            errorMessage = String(describing: error)
        }
    }

    func applySettings() async {
        let generation = connectionGeneration
        guard isConfigured else {
            try? store.savePreferences(preferences, connection: credentials)
            WidgetCenter.shared.reloadAllTimelines()
            return
        }
        do {
            let client = try client()
            var next = settings
            next.providerOrder = preferences.providerOrder
            next.hiddenProviders = preferences.hiddenProviders
            let updated = try await client.updateSettings(next)
            guard connectionGeneration == generation else { return }
            settings = updated
            preferences = DisplayPreferences(
                providerOrder: updated.providerOrder,
                hiddenProviders: updated.hiddenProviders
            )
            try store.savePreferences(preferences, connection: credentials)
            WidgetCenter.shared.reloadAllTimelines()
            errorMessage = nil
            await refresh()
        } catch {
            guard connectionGeneration == generation else { return }
            errorMessage = String(describing: error)
        }
    }

    func saveSettings(_ draft: ServerSettings) async throws {
        let generation = connectionGeneration
        let updated: ServerSettings
        do {
            updated = try await client().updateSettings(draft)
        } catch {
            guard connectionGeneration == generation else { return }
            throw error
        }
        guard connectionGeneration == generation else { return }
        settings = updated
        preferences = DisplayPreferences(providerOrder: updated.providerOrder, hiddenProviders: updated.hiddenProviders)
        try store.savePreferences(preferences, connection: credentials)
        WidgetCenter.shared.reloadAllTimelines()
        await refresh()
    }

    func refreshReadiness() async {
        let generation = connectionGeneration
        do {
            let client = try client()
            let deviceID = store.deviceID()
            async let healthTask = client.fetchHealth()
            async let readinessTask = client.fetchReadiness(deviceID: deviceID)
            let (latestHealth, latestReadiness) = try await (healthTask, readinessTask)
            guard connectionGeneration == generation else { return }
            health = latestHealth
            readiness = latestReadiness
            let local = await UNUserNotificationCenter.current().notificationSettings()
            guard connectionGeneration == generation else { return }
            switch local.authorizationStatus {
            case .authorized: notificationStatus = "authorized"
            case .provisional: notificationStatus = "provisional"
            case .ephemeral: notificationStatus = "ephemeral"
            case .denied: notificationStatus = "denied"
            case .notDetermined: notificationStatus = "not determined"
            @unknown default: notificationStatus = "unknown"
            }
            errorMessage = nil
        } catch {
            guard connectionGeneration == generation else { return }
            errorMessage = String(describing: error)
        }
    }

    func runReadinessTest() async {
        let generation = connectionGeneration
        isTestingAction = true; defer { if connectionGeneration == generation { isTestingAction = false } }
        do {
            let result = try await client().testReadiness(deviceID: store.deviceID())
            guard connectionGeneration == generation else { return }
            statusMessage = result.alertAccepted ? "APNs accepted the device test" : "The device test was not accepted"
            await refreshReadiness()
        } catch {
            guard connectionGeneration == generation else { return }
            errorMessage = String(describing: error)
        }
    }

    func moveProvider(from source: IndexSet, to destination: Int) {
        var order = preferences.providerOrder
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
        let generation = connectionGeneration
        do {
            let client = try client()
            let deviceID = store.deviceID()
            let widget = widgetToken ?? store.pendingWidgetToken()
            let reg = DeviceRegistration(deviceID: deviceID, apnsToken: apnsToken, widgetToken: widget)
            _ = try await client.registerDevice(reg)
            guard connectionGeneration == generation else { return }
            if widget != nil {
                store.setPendingWidgetToken(nil)
            }
            errorMessage = nil
        } catch {
            guard connectionGeneration == generation else { return }
            if isConfigured {
                errorMessage = "Device registration failed: \(error)"
            }
        }
    }

    func forcePoll() async {
        let generation = connectionGeneration
        isTestingAction = true
        defer { if connectionGeneration == generation { isTestingAction = false } }
        do {
            let client = try client()
            let result = try await client.forcePoll()
            guard connectionGeneration == generation else { return }
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
            guard connectionGeneration == generation else { return }
            errorMessage = String(describing: error)
            statusMessage = nil
        }
    }

}
