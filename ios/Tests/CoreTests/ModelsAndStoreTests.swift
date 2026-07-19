import XCTest
@testable import UsageWidget

final class ModelsAndStoreTests: XCTestCase {
    func testProviderLogoAssetNamesUseModelMarks() {
        XCTAssertEqual(ProviderLogoAsset.name(for: "codex"), "ProviderCodex")
        XCTAssertEqual(ProviderLogoAsset.name(for: " Claude "), "ProviderClaude")
        XCTAssertEqual(ProviderLogoAsset.name(for: "GROK"), "ProviderGrok")
        XCTAssertNil(ProviderLogoAsset.name(for: "demo"))
    }

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
            "source":"codexbar-cli-sidecar", "status":"degraded",
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
        XCTAssertEqual(health.collector?.source, "codexbar-cli-sidecar")
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
        let json = #"{"pollIntervalMinutes":5,"providerOrder":["codex"],"hiddenProviders":[],"demoProviderEnabled":false,"notificationsEnabled":true,"earlyThresholdPct":25,"dangerThresholdPct":10}"#.data(using: .utf8)!
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
