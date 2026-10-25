import XCTest
import Security
@testable import UsageWidget

final class ModelsAndStoreTests: XCTestCase {
    func testDecodeFullSnapshot() throws {
        let json = """
        {
          "fetchedAt": "2026-07-17T12:00:00Z",
          "stale": false,
          "pollIntervalMinutes": 5,
          "providers": [
            {
              "id": "codex",
              "name": "Codex",
              "windows": [
                {
                  "id": "codex.primary",
                  "key": "primary",
                  "title": "5h limit",
                  "usedPercent": 42.0,
                  "remainingPercent": 58.0,
                  "resetsAt": "2026-07-17T20:00:00Z"
                }
              ],
              "credits": { "availableCount": 2 },
              "extraFutureField": true
            }
          ]
        }
        """.data(using: .utf8)!

        let snap = try JSONCoding.decoder.decode(Snapshot.self, from: json)
        XCTAssertEqual(snap.providers.count, 1)
        XCTAssertEqual(snap.providers[0].id, "codex")
        XCTAssertEqual(snap.providers[0].windows[0].usedPercent, 42, accuracy: 0.001)
        XCTAssertEqual(snap.providers[0].credits?.availableCount, 2)
        XCTAssertNotNil(snap.providers[0].windows[0].resetsAt)
    }

    func testDecodeNullResetsAndProviderError() throws {
        let json = """
        {
          "fetchedAt": "2026-07-17T12:00:00Z",
          "stale": true,
          "pollIntervalMinutes": 15,
          "providers": [
            {
              "id": "claude",
              "name": "Claude",
              "error": "session expired",
              "windows": [
                {
                  "id": "claude.primary",
                  "key": "primary",
                  "title": "Session",
                  "usedPercent": 0,
                  "remainingPercent": 100
                }
              ]
            }
          ]
        }
        """.data(using: .utf8)!

        let snap = try JSONCoding.decoder.decode(Snapshot.self, from: json)
        XCTAssertTrue(snap.stale)
        XCTAssertEqual(snap.providers[0].error, "session expired")
        XCTAssertNil(snap.providers[0].windows[0].resetsAt)
    }

    func testSnapshotStoreRoundTrip() throws {
        let store = SnapshotStore.temporary()
        let snap = Snapshot(
            fetchedAt: Date(timeIntervalSince1970: 1_721_217_600),
            stale: false,
            providers: [
                Provider(id: "grok", name: "Grok", windows: [
                    UsageWindow(id: "grok.primary", key: "primary", title: "Rate", usedPercent: 5, remainingPercent: 95),
                ]),
            ],
            pollIntervalMinutes: 1
        )
        try store.saveSnapshot(snap)
        let loaded = store.loadSnapshot()
        XCTAssertEqual(loaded?.providers.first?.id, "grok")
        XCTAssertEqual(loaded?.pollIntervalMinutes, 1)

        let prefs = DisplayPreferences(providerOrder: ["grok", "codex"], hiddenProviders: ["claude"])
        try store.savePreferences(prefs)
        let loadedPrefs = store.loadPreferences()
        XCTAssertEqual(loadedPrefs.providerOrder, ["grok", "codex"])
        XCTAssertEqual(loadedPrefs.hiddenProviders, ["claude"])
    }

    func testProviderOrderingAndVisibility() {
        let providers = [
            Provider(id: "a", name: "A"),
            Provider(id: "b", name: "B"),
            Provider(id: "c", name: "C"),
        ]
        let ordered = ProviderDisplay.orderedVisible(
            providers: providers,
            order: ["c", "a"],
            hidden: ["b"]
        )
        XCTAssertEqual(ordered.map(\.id), ["c", "a"])
    }

    func testOrderedVisibleIgnoresOrderLeftoversAndDuplicateIDs() {
        let providers = [
            Provider(id: "codex", name: "Codex"),
            Provider(id: "codex", name: "Codex dup"),
            Provider(id: "grok", name: "Grok"),
        ]
        let ordered = ProviderDisplay.orderedVisible(
            providers: providers,
            order: ["cursor", "codex", "demo"],
            hidden: []
        )
        XCTAssertEqual(ordered.map(\.id), ["codex", "grok"])
        XCTAssertEqual(ordered.first?.name, "Codex")
    }

    func testDefaultProviderOrderIsCatalog() {
        XCTAssertEqual(DisplayPreferences().providerOrder, ["cursor", "codex", "claude_code", "copilot", "gemini_cli", "grok", "devin"])
        XCTAssertEqual(ServerSettings().providerOrder, ProviderCatalog.defaultOrder)
        XCTAssertTrue(ProviderCatalog.defaultOrder.contains("copilot"))
        XCTAssertTrue(ProviderCatalog.defaultOrder.contains("gemini_cli"))
        XCTAssertTrue(ProviderCatalog.defaultOrder.contains("devin"))
    }

    func testDeviceIDStable() {
        let store = SnapshotStore.temporary()
        let a = store.deviceID()
        let b = store.deviceID()
        XCTAssertEqual(a, b)
        XCTAssertFalse(a.isEmpty)
    }

    func testDecodeCollectorAndWidgetHealth() throws {
        let json = """
        {
          "service":"ok", "codexbar":false, "database":true, "polling":true, "apns":true,
          "collector": {
            "source":"crossusage-collector", "status":"degraded",
            "lastAttemptAt":"2026-07-18T10:00:00Z", "lastSuccessAt":"2026-07-18T09:55:00Z",
            "durationMs":720, "consecutiveFailures":1, "lastError":"collector rate limited"
          },
          "widgetDelivery": {
            "status":"warning", "attempted":1, "succeeded":0, "failed":1,
            "lastError":"InvalidProviderToken"
          }
        }
        """.data(using: .utf8)!
        let health = try JSONCoding.decoder.decode(Health.self, from: json)
        XCTAssertEqual(health.collector?.source, "crossusage-collector")
        XCTAssertEqual(health.collector?.consecutiveFailures, 1)
        XCTAssertEqual(health.widgetDelivery?.failed, 1)
    }

    func testDecodeLegacyHealthWithoutDiagnostics() throws {
        let json = """
        {"service":"ok","codexbar":true,"database":true,"polling":true,"apns":false}
        """.data(using: .utf8)!
        let health = try JSONCoding.decoder.decode(Health.self, from: json)
        XCTAssertNil(health.collector)
        XCTAssertNil(health.widgetDelivery)
    }

    func testDecodeLegacySettingsUsesNewDefaults() throws {
        let json = #"{"pollIntervalMinutes":5,"providerOrder":["codex"],"hiddenProviders":[],"notificationsEnabled":true,"earlyThresholdPct":25,"dangerThresholdPct":10}"#.data(using: .utf8)!
        let settings = try JSONCoding.decoder.decode(ServerSettings.self, from: json)
        XCTAssertEqual(settings.defaultRepeatIntervalMinutes, 0)
        XCTAssertFalse(settings.quietHours.enabled)
        XCTAssertTrue(settings.alertOverrides.isEmpty)
    }

    func testForecastDecodeAndFormatting() throws {
        let json = """
        {"fetchedAt":"2026-07-19T00:00:00Z","stale":false,"pollIntervalMinutes":5,"providers":[{"id":"codex","name":"Codex","windows":[{"id":"codex.primary","key":"primary","title":"5h","usedPercent":50,"remainingPercent":50,"resetsAt":"2026-07-19T05:00:00Z","forecast":{"computedAt":"2026-07-19T00:00:00Z","burnRatePercentPerHour":20,"estimatedExhaustionAt":"2026-07-19T02:30:00Z","exhaustsBeforeReset":true,"sampleCount":4,"basedOnHours":1}}]}]}
        """.data(using: .utf8)!
        let snap = try JSONCoding.decoder.decode(Snapshot.self, from: json)
        XCTAssertEqual(snap.providers[0].windows[0].forecast?.sampleCount, 4)
        XCTAssertTrue(ForecastText.string(for: snap.providers[0].windows[0], now: snap.fetchedAt)?.hasPrefix("Likely out") == true)
    }

    func testAlertInheritance() {
        var settings = ServerSettings(earlyThresholdPct: 10)
        settings.alertOverrides = [
            AlertOverride(providerID: "codex", windowID: nil, rule: AlertRule(earlyThresholdPct: 20)),
            AlertOverride(providerID: "codex", windowID: "codex.primary", rule: AlertRule(enabled: false, earlyThresholdPct: 30)),
        ]
        XCTAssertEqual(settings.effectiveRule(providerID: "codex", windowID: "codex.secondary").earlyThresholdPct, 20)
        XCTAssertFalse(settings.effectiveRule(providerID: "codex", windowID: "codex.primary").enabled)
        XCTAssertEqual(settings.effectiveRule(providerID: "claude").earlyThresholdPct, 10)
    }

    func testQRConfigurationRoundTripAndValidation() throws {
        let token = String(repeating: "a", count: 64)
        let server = "https%3A%2F%2Fhost.example.ts.net%2Fusagewidget"
        let parsed = try QRConfiguration.parse("usagewidget://configure?v=1&server=\(server)&token=\(token)")
        XCTAssertEqual(parsed.serverURL, "https://host.example.ts.net/usagewidget")
        XCTAssertThrowsError(try QRConfiguration.parse("usagewidget://configure?v=2&server=\(server)&token=\(token)"))
        XCTAssertThrowsError(try QRConfiguration.parse("usagewidget://configure?v=1&server=http%3A%2F%2Fhost%2Fusagewidget&token=\(token)"))
        XCTAssertThrowsError(try QRConfiguration.parse("usagewidget://other?v=1&server=\(server)&token=\(token)"))
    }
}

final class APIClientRequestTests: XCTestCase {
    func testBuildsAuthHeaderAndPaths() throws {
        let base = URL(string: "https://edserve.example.ts.net/usagewidget")!
        let client = APIClient(baseURL: base, token: "secret-token", session: .shared)
        let req = try client.makeRequest(path: "/v1/snapshot", method: "GET")
        XCTAssertEqual(req.httpMethod, "GET")
        XCTAssertEqual(req.value(forHTTPHeaderField: "Authorization"), "Bearer secret-token")
        XCTAssertEqual(req.url?.absoluteString, "https://edserve.example.ts.net/usagewidget/v1/snapshot")
    }

    func testNormalizedBaseURLStripsTrailingSlash() {
        let url = APIClient.normalizedBaseURL("https://host.example/usagewidget/")
        XCTAssertEqual(url?.absoluteString, "https://host.example/usagewidget")
    }
}

/// Serves canned failures keyed by host so APIClient can be driven through its
/// real request/response/decoding path without a network or shared mutable state.
final class FailureStubProtocol: URLProtocol {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let url = request.url!
        func respond(_ status: Int, _ body: String) {
            let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: nil)!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(body.utf8))
            client?.urlProtocolDidFinishLoading(self)
        }
        switch url.host {
        case "unauthorized.test": respond(401, #"{"error":"unauthorized"}"#)
        case "tunnel-down.test": respond(530, "error code: 1033")
        case "boom.test": respond(500, "panic: secret stack trace")
        case "garbage.test": respond(200, "<html>captive portal</html>")
        case "missing.test": respond(404, "404 page not found")
        default: client?.urlProtocol(self, didFailWithError: URLError(.cannotFindHost))
        }
    }

    override func stopLoading() {}
}

final class FriendlyErrorTests: XCTestCase {
    private func stubbedClient(host: String) -> APIClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [FailureStubProtocol.self]
        return APIClient(baseURL: URL(string: "https://\(host)/usagewidget")!, token: "t", session: URLSession(configuration: config), timeout: 5)
    }

    private func widgetMessage(host: String) async -> String {
        do {
            _ = try await stubbedClient(host: host).fetchSnapshot()
            return "no error"
        } catch {
            return FriendlyError.message(for: error)
        }
    }

    func testAPIClientFailuresProduceFriendlyWidgetText() async {
        let cases: [(host: String, expected: String)] = [
            ("unauthorized.test", "Token rejected"),
            ("tunnel-down.test", "Can't reach server"),
            ("unreachable.test", "Can't reach server"),
            ("boom.test", "Server error (500)"),
            ("garbage.test", "Unexpected server response"),
            ("missing.test", "Server path not found"),
        ]
        for c in cases {
            let message = await widgetMessage(host: c.host)
            XCTAssertEqual(message, c.expected, "host \(c.host)")
        }
    }

    func testConcreteErrorsMapToConcreteStrings() {
        let cases: [(Error, String)] = [
            (APIError.httpStatus(401, nil), "Token rejected"),
            (APIError.httpStatus(403, "forbidden"), "Token rejected"),
            (APIError.httpStatus(502, nil), "Can't reach server"),
            (APIError.httpStatus(418, nil), "Server error (418)"),
            (APIError.transport("The request timed out."), "Can't reach server"),
            (APIError.invalidBaseURL, "Not set up"),
            (APIError.invalidResponse, "Unexpected server response"),
            (APIError.decoding("keyNotFound(providers)"), "Unexpected server response"),
            (KeychainError.missingValue, "Not set up"),
            (KeychainError.unexpectedStatus(errSecItemNotFound), "Not set up"),
            (KeychainError.unexpectedStatus(errSecInteractionNotAllowed), "Unlock iPhone to refresh"),
            (URLError(.timedOut), "Can't reach server"),
            (CocoaError(.fileReadCorruptFile), "Couldn't refresh"),
        ]
        for (error, expected) in cases {
            XCTAssertEqual(FriendlyError.message(for: error), expected, "\(error)")
        }
    }

    func testRealDecodingFailureIsUnexpectedResponse() {
        do {
            _ = try JSONCoding.decoder.decode(Snapshot.self, from: Data(#"{"stale":false}"#.utf8))
            XCTFail("decode should fail")
        } catch {
            XCTAssertEqual(FriendlyError.message(for: error), "Unexpected server response")
        }
    }

    func testMessagesNeverEchoServerBodiesAndStayShort() {
        let leaky: [Error] = [
            APIError.httpStatus(401, "Bearer usagewidget-secret-token rejected"),
            APIError.httpStatus(500, "panic: /home/user/.config/usagewidget/env"),
            APIError.decoding("typeMismatch(Swift.Double, Swift.DecodingError.Context(codingPath: ...))"),
            APIError.transport("A server with the specified hostname could not be found."),
        ]
        for error in leaky {
            let message = FriendlyError.message(for: error)
            XCTAssertFalse(message.contains("secret"), message)
            XCTAssertFalse(message.contains("/home"), message)
            XCTAssertFalse(message.contains("Swift."), message)
            XCTAssertFalse(message.contains("hostname"), message)
            XCTAssertLessThanOrEqual(message.count, 28, "widget line too long: \(message)")
        }
    }
}

final class DeliveryStateTests: XCTestCase {
    /// What usagewidgetd returns from /v1/health and /v1/readiness when no APNs key is configured.
    private let dashboardOnlyHealthJSON = #"{"service":"ok","codexbar":true,"database":true,"polling":true,"apns":false}"#
    private let dashboardOnlyReadinessJSON = """
    {"ready":false,"checkedAt":"2026-10-08T03:00:00Z","checks":[
      {"id":"collector","title":"Collector","status":"pass","detail":"Collector is healthy","core":true},
      {"id":"snapshot","title":"Latest snapshot","status":"pass","detail":"The latest snapshot is current","core":true},
      {"id":"apns","title":"APNs configuration","status":"fail","detail":"APNs is not configured; dashboard-only mode is available","core":true},
      {"id":"device","title":"Device registration","status":"pass","detail":"This device is registered","core":true},
      {"id":"alert_token","title":"Alert token","status":"fail","detail":"No alert token is registered","core":true},
      {"id":"widget_token","title":"Widget token","status":"warning","detail":"No widget push token is registered","core":true},
      {"id":"delivery_test","title":"Recent device test","status":"fail","detail":"No device-specific test has been recorded","core":true}
    ]}
    """

    private func health(apns: Bool) -> Health {
        Health(service: "ok", codexbar: true, database: true, polling: true, apns: apns)
    }

    private func readiness(ready: Bool) throws -> Readiness {
        let json = #"{"ready":\#(ready),"checkedAt":"2026-10-08T03:00:00Z","checks":[]}"#
        return try JSONCoding.decoder.decode(Readiness.self, from: Data(json.utf8))
    }

    func testServerWithoutAPNsIsDashboardOnlyNotNeedsAttention() throws {
        let health = try JSONCoding.decoder.decode(Health.self, from: Data(dashboardOnlyHealthJSON.utf8))
        let readiness = try JSONCoding.decoder.decode(Readiness.self, from: Data(dashboardOnlyReadinessJSON.utf8))
        XCTAssertFalse(readiness.ready, "server marks readiness false without APNs")
        for authorized in [false, true] {
            let state = DeliveryState.evaluate(health: health, readiness: readiness, notificationsAuthorized: authorized)
            XCTAssertEqual(state, .dashboardOnly)
            XCTAssertNotEqual(state.title, "Needs attention")
        }
        // Also before readiness has loaded: health alone decides.
        XCTAssertEqual(DeliveryState.evaluate(health: health, readiness: nil, notificationsAuthorized: false), .dashboardOnly)
        XCTAssertFalse(DeliveryState.offersNotificationPermission(health: health))
    }

    func testDashboardOnlyHidesPushOnlyChecksButKeepsFixableOnes() throws {
        let readiness = try JSONCoding.decoder.decode(Readiness.self, from: Data(dashboardOnlyReadinessJSON.utf8))
        XCTAssertEqual(DeliveryState.dashboardOnly.relevantChecks(readiness.checks).map(\.id), ["collector", "snapshot", "device"])
        XCTAssertEqual(DeliveryState.needsAttention.relevantChecks(readiness.checks).count, 7)
    }

    func testAPNsServerStates() throws {
        let push = health(apns: true)
        XCTAssertEqual(DeliveryState.evaluate(health: push, readiness: try readiness(ready: true), notificationsAuthorized: true), .ready)
        XCTAssertEqual(DeliveryState.evaluate(health: push, readiness: try readiness(ready: true), notificationsAuthorized: false), .needsAttention)
        XCTAssertEqual(DeliveryState.evaluate(health: push, readiness: try readiness(ready: false), notificationsAuthorized: true), .needsAttention)
        XCTAssertEqual(DeliveryState.evaluate(health: push, readiness: nil, notificationsAuthorized: true), .checking)
        XCTAssertTrue(DeliveryState.offersNotificationPermission(health: push))
    }

    func testUnknownHealthIsCheckingAndStillOffersPermission() {
        XCTAssertEqual(DeliveryState.evaluate(health: nil, readiness: nil, notificationsAuthorized: false), .checking)
        XCTAssertTrue(DeliveryState.offersNotificationPermission(health: nil))
    }

    func testDashboardOnlyCopyExplainsWidgetScheduleAndNoAlerts() {
        XCTAssertEqual(DeliveryState.dashboardOnly.title, "Dashboard-only")
        let text = DeliveryState.dashboardOnly.explanation
        XCTAssertTrue(text.contains("widget refreshes on its own schedule"), text)
        XCTAssertTrue(text.contains("no alerts"), text)
        XCTAssertTrue(text.contains("APNs"), text)
    }
}

private final class InMemoryCredentialStore: CredentialStoring {
    var stored: ConnectionCredentials?
    init(_ stored: ConnectionCredentials?) { self.stored = stored }
    func save(_ credentials: ConnectionCredentials) throws { stored = credentials }
    func load() throws -> ConnectionCredentials? { stored }
}

@MainActor
final class AppModelCredentialTests: XCTestCase {
    func testLoadingKeychainCredentialsClearsLegacyMirror() throws {
        let name = "usagewidget.tests.\(UUID().uuidString)"
        let store = SnapshotStore(suiteName: name)
        let defaults = try XCTUnwrap(UserDefaults(suiteName: name))
        defer { defaults.removePersistentDomain(forName: name) }
        let credentials = ConnectionCredentials(serverURL: "https://current.example", token: "current-test-token")
        let keychain = InMemoryCredentialStore(credentials)
        store.mirrorCredentials(ConnectionCredentials(serverURL: "https://legacy.example", token: "legacy-test-token"))
        XCTAssertNotNil(defaults.object(forKey: "mirroredServerURL"))
        XCTAssertNotNil(defaults.object(forKey: "mirroredBearerToken"))

        let model = AppModel(keychain: keychain, store: store)

        XCTAssertTrue(model.isConfigured)
        XCTAssertEqual(model.credentials, credentials)
        XCTAssertEqual(keychain.stored, credentials)
        XCTAssertNil(defaults.object(forKey: "mirroredServerURL"))
        XCTAssertNil(defaults.object(forKey: "mirroredBearerToken"))
    }
}

@MainActor
final class AppModelDeliveryTests: XCTestCase {
    private func makeModel() -> AppModel {
        AppModel(
            keychain: KeychainStore(service: "usagewidget.tests.\(UUID().uuidString)", accessGroup: nil),
            store: SnapshotStore.temporary()
        )
    }

    func testModelUsesHealthToPickDashboardOnly() {
        let model = makeModel()
        model.notificationStatus = "denied"
        XCTAssertEqual(model.deliveryState, .checking)
        XCTAssertTrue(model.offersNotificationPermission)

        model.health = Health(service: "ok", codexbar: true, database: true, polling: true, apns: false)
        XCTAssertEqual(model.deliveryState, .dashboardOnly)
        XCTAssertFalse(model.offersNotificationPermission)

        model.health?.apns = true
        XCTAssertEqual(model.deliveryState, .checking, "APNs server still needs readiness before Ready/Needs attention")
        XCTAssertTrue(model.offersNotificationPermission)
    }

    func testNotificationsAuthorizedMatchesLocalStatuses() {
        let model = makeModel()
        for (status, expected) in [("authorized", true), ("provisional", true), ("ephemeral", true), ("denied", false), ("not determined", false), ("unknown", false)] {
            model.notificationStatus = status
            XCTAssertEqual(model.notificationsAuthorized, expected, status)
        }
    }
}

/// Models atomic Security operations, not whole save/load transactions, so separate
/// stores can still interleave their migration and save operations in these tests.
private final class InMemoryKeychainItems: KeychainItemStoring, @unchecked Sendable {
    private struct Key: Hashable {
        let itemClass: String?
        let service: String?
        let accessGroup: String?
        let account: String?

        init(_ query: [String: Any]) {
            itemClass = query[kSecClass as String] as? String
            service = query[kSecAttrService as String] as? String
            accessGroup = query[kSecAttrAccessGroup as String] as? String
            account = query[kSecAttrAccount as String] as? String
        }
    }

    private struct Item {
        var data: Data
        let reference = Data(UUID().uuidString.utf8)
    }

    private let lock = NSLock()
    private var items: [Key: Item] = [:]

    func add(_ query: [String: Any]) -> OSStatus {
        lock.lock()
        defer { lock.unlock() }
        let key = Key(query)
        guard items[key] == nil else { return errSecDuplicateItem }
        guard let data = query[kSecValueData as String] as? Data else { return errSecParam }
        items[key] = Item(data: data)
        return errSecSuccess
    }

    func update(_ query: [String: Any], attributes: [String: Any]) -> OSStatus {
        lock.lock()
        defer { lock.unlock() }
        let key = Key(query)
        guard var item = items[key] else { return errSecItemNotFound }
        guard let data = attributes[kSecValueData as String] as? Data else { return errSecParam }
        item.data = data
        items[key] = item
        return errSecSuccess
    }

    func copyMatching(_ query: [String: Any]) -> (OSStatus, Data?) {
        lock.lock()
        defer { lock.unlock() }
        guard let item = items[Key(query)] else { return (errSecItemNotFound, nil) }
        if query[kSecReturnPersistentRef as String] as? Bool == true {
            return (errSecSuccess, item.reference)
        }
        return (errSecSuccess, item.data)
    }

    func delete(_ query: [String: Any]) -> OSStatus {
        lock.lock()
        defer { lock.unlock() }
        return items.removeValue(forKey: Key(query)) == nil ? errSecItemNotFound : errSecSuccess
    }
}

final class KeychainPairingTests: XCTestCase {
    private var service = ""
    private var store: KeychainStore!
    private var items: InMemoryKeychainItems!

    override func setUp() {
        super.setUp()
        service = "usagewidget.pairing.tests.\(UUID().uuidString)"
        items = InMemoryKeychainItems()
        store = makeStore()
    }

    override func tearDownWithError() throws {
        try store.clear()
        store = nil
        items = nil
        try super.tearDownWithError()
    }

    private func makeStore() -> KeychainStore {
        KeychainStore(service: service, accessGroup: nil, items: items)
    }

    private func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: account]
    }

    private func seedLegacy(_ credentials: ConnectionCredentials) throws {
        for (account, value) in [("serverURL", credentials.serverURL), ("bearerToken", credentials.token)] {
            var item = query(account)
            item[kSecValueData as String] = Data(value.utf8)
            item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
            let status = items.add(item)
            guard status == errSecSuccess else { throw KeychainError.unexpectedStatus(status) }
        }
    }

    private func pairReference() throws -> Data {
        var item = query("connectionCredentials")
        item[kSecReturnPersistentRef as String] = true
        let (status, result) = items.copyMatching(item)
        guard status == errSecSuccess, let reference = result else {
            throw KeychainError.unexpectedStatus(status)
        }
        return reference
    }

    private func assertLegacyAbsent() {
        for account in ["serverURL", "bearerToken"] {
            XCTAssertEqual(items.copyMatching(query(account)).0, errSecItemNotFound)
        }
    }

    func testSavingUpdatesOneItemWithoutReplacingIt() throws {
        let first = ConnectionCredentials(serverURL: "https://a.example", token: "test-a")
        let second = ConnectionCredentials(serverURL: "https://b.example", token: "test-b")
        try store.save(first)
        let reference = try pairReference()
        try store.save(second)
        XCTAssertEqual(try pairReference(), reference)
        XCTAssertTrue(try store.load() == second, "Saved URL and token must round-trip together")
        assertLegacyAbsent()
    }

    func testLegacyMigrationPersistsPairAndRemovesSeparateItems() throws {
        let legacy = ConnectionCredentials(serverURL: "https://legacy.example", token: "test-legacy")
        try seedLegacy(legacy)
        XCTAssertTrue(try store.load() == legacy)
        _ = try pairReference()
        assertLegacyAbsent()
        let widget = makeStore()
        XCTAssertTrue(try widget.load() == legacy)
        try store.clear()
        XCTAssertNil(try widget.load())
    }

    func testExistingPairTakesPrecedenceOverLegacyItems() throws {
        let current = ConnectionCredentials(serverURL: "https://current.example", token: "test-current")
        try store.save(current)
        try seedLegacy(ConnectionCredentials(serverURL: "https://old.example", token: "test-old"))
        XCTAssertTrue(try store.load() == current)
        try store.clear()
        assertLegacyAbsent()
        XCTAssertNil(try store.load())
    }

    func testIncompleteLegacyPairIsNotReturned() throws {
        try seedLegacy(ConnectionCredentials(serverURL: "https://old.example", token: "test-old"))
        XCTAssertEqual(items.delete(query("bearerToken")), errSecSuccess)
        XCTAssertNil(try store.load())
    }

    func testConcurrentServerChangesNeverReturnMixedOrMissingCredentials() throws {
        let first = ConnectionCredentials(serverURL: "https://a.example", token: "test-a")
        let second = ConnectionCredentials(serverURL: "https://b.example", token: "test-b")
        try store.save(first)
        // Separate stores share only the backend's per-operation lock, not a transaction lock.
        let writer = makeStore()
        let reader = makeStore()
        DispatchQueue.concurrentPerform(iterations: 200) { iteration in
            do {
                if iteration.isMultiple(of: 2) {
                    try writer.save(iteration.isMultiple(of: 4) ? first : second)
                } else {
                    let loaded = try reader.load()
                    XCTAssertTrue(loaded == first || loaded == second,
                                  "Every read must return a complete saved pair")
                }
            } catch {
                XCTFail("Concurrent credential access failed")
            }
        }
    }

    func testConcurrentMigrationCannotOverwriteNewServerCredentials() throws {
        let legacy = ConnectionCredentials(serverURL: "https://old.example", token: "test-old")
        let current = ConnectionCredentials(serverURL: "https://new.example", token: "test-new")
        let writer = makeStore()
        let reader = makeStore()
        for _ in 0..<30 {
            try store.clear()
            try seedLegacy(legacy)
            DispatchQueue.concurrentPerform(iterations: 2) { iteration in
                do {
                    if iteration == 0 {
                        try writer.save(current)
                    } else {
                        let loaded = try reader.load()
                        XCTAssertTrue(loaded == legacy || loaded == current,
                                      "Migration must return a complete saved pair")
                    }
                } catch {
                    XCTFail("Concurrent migration failed")
                }
            }
            XCTAssertTrue(try store.load() == current, "Migration must not overwrite a newer save")
            assertLegacyAbsent()
        }
    }
}

final class ConnectionCacheTests: XCTestCase {
    func testCacheSeparatesURLsAndTokenRotationsAndSurvivesRelaunch() throws {
        let name = "usagewidget.tests.\(UUID().uuidString)"
        let store = SnapshotStore(suiteName: name)
        defer { UserDefaults(suiteName: name)?.removePersistentDomain(forName: name) }
        let first = ConnectionCredentials(serverURL: "https://a.example", token: "test-a")
        let otherURL = ConnectionCredentials(serverURL: "https://b.example", token: "test-a")
        let otherToken = ConnectionCredentials(serverURL: first.serverURL, token: "test-b")
        let prefs = DisplayPreferences(providerOrder: ["codex"], hiddenProviders: ["grok"])
        try store.saveSnapshot(SampleCapacity.snapshot, connection: first)
        try store.savePreferences(prefs, connection: first)
        let relaunched = SnapshotStore(suiteName: name)
        XCTAssertNotNil(relaunched.loadSnapshot(connection: first))
        XCTAssertEqual(relaunched.loadPreferences(connection: first), prefs)
        for connection in [otherURL, otherToken] {
            XCTAssertNil(relaunched.loadSnapshot(connection: connection))
            XCTAssertEqual(relaunched.loadPreferences(connection: connection), DisplayPreferences())
        }
        // Old installations have no evidence of which connection wrote these values.
        let defaults = try XCTUnwrap(UserDefaults(suiteName: name))
        defaults.set(try JSONCoding.encoder.encode(SampleCapacity.snapshot), forKey: "snapshot.json")
        defaults.set(try JSONCoding.encoder.encode(prefs), forKey: "displayPreferences.json")
        XCTAssertNil(relaunched.loadSnapshot(connection: otherURL))
        XCTAssertEqual(relaunched.loadPreferences(connection: otherURL), DisplayPreferences())
        XCTAssertNil(relaunched.loadSnapshot())
    }
}

private actor ConnectionResponseGate {
    private var continuation: CheckedContinuation<Void, Never>?
    private var released = false

    func wait() async {
        if released { return }
        await withCheckedContinuation { continuation = $0 }
    }

    func release() {
        released = true
        continuation?.resume()
        continuation = nil
    }
}

private actor ConnectionTestRoutes {
    typealias Handler = @Sendable (URLRequest) async -> (Int, Data)
    private var handlers: [String: Handler] = [:]
    func set(_ host: String, handler: @escaping Handler) { handlers[host] = handler }
    func respond(_ request: URLRequest) async -> (Int, Data) {
        guard let handler = handlers[request.url!.host!] else { return (500, Data()) }
        return await handler(request)
    }
}

private final class ConnectionTestProtocol: URLProtocol {
    // URLProtocol's Sendable conformance is unavailable in the Apple SDK.
    // Transfer only this load's callback target; one task delivers its response,
    // and stopLoading does not access or mutate any task-owned state.
    private struct ResponseTarget: @unchecked Sendable {
        let protocolInstance: ConnectionTestProtocol

        func finish(status: Int, data: Data, url: URL) {
            let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: nil, headerFields: nil)!
            let client = protocolInstance.client
            client?.urlProtocol(protocolInstance, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(protocolInstance, didLoad: data)
            client?.urlProtocolDidFinishLoading(protocolInstance)
        }
    }

    static let routes = ConnectionTestRoutes()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let request = request
        let target = ResponseTarget(protocolInstance: self)
        Task {
            let (status, data) = await ConnectionTestProtocol.routes.respond(request)
            target.finish(status: status, data: data, url: request.url!)
        }
    }
    override func stopLoading() {}
}

@MainActor
final class AppModelConnectionCacheTests: XCTestCase {
    func testSwitchWithFailedRefreshDoesNotDisplayPreviousCache() async throws {
        let session = makeSession()
        defer { session.invalidateAndCancel() }
        let first = ConnectionCredentials(serverURL: "https://\(UUID().uuidString).example", token: "test-a")
        let second = ConnectionCredentials(serverURL: "https://\(UUID().uuidString).example", token: "test-b")
        let store = SnapshotStore.temporary()
        try store.saveSnapshot(SampleCapacity.snapshot, connection: first)
        try store.savePreferences(DisplayPreferences(hiddenProviders: ["codex"]), connection: first)
        let model = AppModel(keychain: InMemoryCredentialStore(first), store: store, session: session)
        XCTAssertNotNil(model.snapshot)
        await route(second, failSnapshot: true)
        try await model.saveConnection(url: second.serverURL, token: second.token)
        XCTAssertNil(model.snapshot)
        XCTAssertEqual(model.preferences, DisplayPreferences())
        XCTAssertNil(store.loadSnapshot(connection: second))
        XCTAssertNotNil(store.loadSnapshot(connection: first))
    }

    func testOldRefreshSuccessAndFailureCannotOverwriteNewConnection() async throws {
        for failOld in [false, true] {
            try await checkDelayedResponse(path: "/v1/snapshot", failOld: failOld)
        }
    }

    func testOldSettingsResponseCannotOverwriteNewConnection() async throws {
        try await checkDelayedResponse(path: "/v1/settings", failOld: false)
    }

    private func checkDelayedResponse(path: String, failOld: Bool) async throws {
        let session = makeSession()
        defer { session.invalidateAndCancel() }
        let first = ConnectionCredentials(serverURL: "https://\(UUID().uuidString).example", token: "test-a")
        let second = ConnectionCredentials(serverURL: "https://\(UUID().uuidString).example", token: "test-b")
        let store = SnapshotStore.temporary()
        let model = AppModel(keychain: InMemoryCredentialStore(first), store: store, session: session)
        let gate = ConnectionResponseGate()
        let started = expectation(description: "Old response held in flight")
        await route(first, failSnapshot: failOld, heldPath: path, gate: gate, started: started)
        await route(second)
        let oldRequest = Task {
            if path == "/v1/settings" {
                await model.applySettings()
            } else {
                await model.refresh()
            }
        }
        let result = await XCTWaiter.fulfillment(of: [started], timeout: 3)
        XCTAssertEqual(result, .completed)
        try await model.saveConnection(url: second.serverURL, token: second.token)
        XCTAssertEqual(model.snapshot?.providers.map(\.id), [URL(string: second.serverURL)!.host!])
        XCTAssertNil(model.errorMessage)
        let expectedProviders = model.snapshot?.providers.map(\.id)
        let expectedPreferences = model.preferences
        model.errorMessage = "Current connection message"
        await gate.release()
        await oldRequest.value
        XCTAssertEqual(model.snapshot?.providers.map(\.id), expectedProviders)
        XCTAssertEqual(model.preferences, expectedPreferences)
        XCTAssertEqual(model.errorMessage, "Current connection message")
        XCTAssertEqual(store.loadSnapshot(connection: second)?.providers.map(\.id), expectedProviders)
        XCTAssertEqual(store.loadPreferences(connection: second), expectedPreferences)
        XCTAssertNil(store.loadSnapshot(connection: first))
    }

    private func makeSession() -> URLSession {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [ConnectionTestProtocol.self]
        return URLSession(configuration: config)
    }

    private func route(_ connection: ConnectionCredentials, failSnapshot: Bool = false,
                       heldPath: String? = nil, gate: ConnectionResponseGate? = nil,
                       started: XCTestExpectation? = nil) async {
        let host = URL(string: connection.serverURL)!.host!
        await ConnectionTestProtocol.routes.set(host) { request in
            let path = request.url!.path
            if path == heldPath {
                started?.fulfill()
                await gate?.wait()
            }
            switch path {
            case "/v1/health":
                return (200, Data(#"{"service":"ok","codexbar":true,"database":true,"polling":true,"apns":false}"#.utf8))
            case "/v1/snapshot":
                if failSnapshot { return (503, Data()) }
                let snapshot = Snapshot(fetchedAt: Date(), stale: false,
                                        providers: [Provider(id: host, name: "Test")], pollIntervalMinutes: 5)
                return (200, try! JSONCoding.encoder.encode(snapshot))
            case "/v1/settings":
                var settings = ServerSettings()
                settings.providerOrder = [host]
                settings.hiddenProviders = [host]
                return (200, try! JSONCoding.encoder.encode(settings))
            case "/v1/devices":
                return (200, try! JSONCoding.encoder.encode(DeviceRegistration(deviceID: "test-device")))
            default:
                return (404, Data())
            }
        }
    }
}
