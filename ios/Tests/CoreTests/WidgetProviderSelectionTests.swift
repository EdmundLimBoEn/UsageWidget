import XCTest
@testable import UsageWidget

final class WidgetProviderSelectionTests: XCTestCase {
    private let providers = [
        Provider(id: "codex", name: "Codex"),
        Provider(id: "cursor", name: "Cursor"),
        Provider(id: "grok", name: "Grok"),
        Provider(id: "claude_code", name: "Claude Code"),
        Provider(id: "devin", name: "Devin"),
    ]

    func testUnconfiguredWidgetUsesSavedOrderAndHiddenChoices() throws {
        let legacyJSON = #"{"providerOrder":["grok","cursor","codex"],"hiddenProviders":["cursor"]}"#.data(using: .utf8)!
        let prefs = try JSONCoding.decoder.decode(DisplayPreferences.self, from: legacyJSON)
        XCTAssertEqual(
            WidgetProviderSelection.providers(from: providers, preferences: prefs).map(\.id),
            ["grok", "codex", "claude_code", "devin"]
        )
    }

    func testDefaultIntentFollowsAppAndIgnoresUnusedCustomSelection() {
        var intent = ProviderWidgetIntent()
        XCTAssertTrue(intent.useAppProviders)
        XCTAssertNil(intent.selectedIDs)
        intent.providers = [WidgetProviderEntity(id: "grok", name: "Grok")]
        XCTAssertNil(intent.selectedIDs)
        intent.useAppProviders = false
        XCTAssertEqual(intent.selectedIDs, ["grok"])
    }

    func testFreshPreferencesKeepCatalogOrderWithoutInventingProviders() {
        let selected = WidgetProviderSelection.providers(from: Array(providers.prefix(2)), preferences: DisplayPreferences())
        XCTAssertEqual(selected.map(\.id), ["cursor", "codex"])
        XCTAssertTrue(WidgetProviderSelection.providers(from: [], preferences: DisplayPreferences()).isEmpty)
    }

    func testCustomSelectionKeepsItsOrderAndDeduplicates() {
        let selected = WidgetProviderSelection.providers(
            from: providers + [Provider(id: "grok", name: "Duplicate")],
            preferences: DisplayPreferences(),
            selectedIDs: ["grok", "codex", "grok"]
        )
        XCTAssertEqual(selected.map(\.id), ["grok", "codex"])
        XCTAssertEqual(selected.first?.name, "Grok")
    }

    func testDuplicateSavedOrderDoesNotDuplicateRowsOrPickerEntities() {
        let selected = WidgetProviderSelection.providers(
            from: providers, preferences: DisplayPreferences(providerOrder: ["codex", "codex", "grok"])
        )
        XCTAssertEqual(selected.map(\.id), ["codex", "grok", "cursor", "claude_code", "devin"])
    }

    func testCustomSelectionRespectsHiddenAndUnavailableProvidersWithoutFallback() {
        let prefs = DisplayPreferences(hiddenProviders: ["grok"])
        XCTAssertEqual(
            WidgetProviderSelection.providers(from: providers, preferences: prefs, selectedIDs: ["missing", "grok", "codex"]).map(\.id),
            ["codex"]
        )
        XCTAssertTrue(WidgetProviderSelection.providers(from: providers, preferences: prefs, selectedIDs: ["grok", "missing"]).isEmpty)
    }

    func testEmptyCustomSelectionIsDistinctFromDefaults() {
        var intent = ProviderWidgetIntent()
        intent.useAppProviders = false
        XCTAssertEqual(intent.selectedIDs, [])
        XCTAssertTrue(WidgetProviderSelection.providers(from: providers, preferences: DisplayPreferences(), selectedIDs: intent.selectedIDs).isEmpty)
        intent.providers = []
        XCTAssertEqual(intent.selectedIDs, [])
    }

    func testTwoInstancesKeepIndependentSelectionsAndLeaveSharedPreferencesAlone() throws {
        let store = SnapshotStore.temporary()
        let prefs = DisplayPreferences(providerOrder: ["grok", "codex"], hiddenProviders: ["cursor"])
        try store.savePreferences(prefs)
        let snapshot = Snapshot(fetchedAt: Date(timeIntervalSince1970: 1_721_217_600), stale: true, providers: providers, pollIntervalMinutes: 5)
        try store.saveSnapshot(snapshot)
        let first = WidgetProviderSelection.providers(from: snapshot.providers, preferences: store.loadPreferences(), selectedIDs: ["codex"])
        let second = WidgetProviderSelection.providers(from: snapshot.providers, preferences: store.loadPreferences(), selectedIDs: ["grok", "devin"])
        XCTAssertEqual(first.map(\.id), ["codex"])
        XCTAssertEqual(second.map(\.id), ["grok", "devin"])
        XCTAssertEqual(store.loadPreferences(), prefs)
        XCTAssertEqual(store.loadSnapshot(), snapshot)
    }

    func testPickerUsesCachedVisibleProvidersInAppOrderWithoutWritingCache() async throws {
        let store = SnapshotStore.temporary()
        let snapshot = Snapshot(fetchedAt: Date(timeIntervalSince1970: 1_721_217_600), stale: true, providers: providers, pollIntervalMinutes: 5)
        try store.saveSnapshot(snapshot)
        try store.savePreferences(DisplayPreferences(providerOrder: ["grok", "codex"], hiddenProviders: ["cursor"]))
        let entities = try await WidgetProviderQuery(store: store).suggestedEntities()
        XCTAssertEqual(entities.map(\.id), ["grok", "codex", "claude_code", "devin"])
        XCTAssertEqual(entities.first?.name, "Grok")
        XCTAssertEqual(store.loadSnapshot(), snapshot)
    }

    func testPickerWithNoCacheDoesNotOfferSampleProviders() async throws {
        let store = SnapshotStore.temporary()
        let entities = try await WidgetProviderQuery(store: store).suggestedEntities()
        XCTAssertTrue(entities.isEmpty)
        XCTAssertNil(store.loadSnapshot())
    }

    func testSavedEntityIdentifiersSurviveTemporaryAbsenceAndReturn() async throws {
        let store = SnapshotStore.temporary()
        let query = WidgetProviderQuery(store: store)
        let missing = try await query.entities(for: ["codex"])
        XCTAssertEqual(missing.map(\.id), ["codex"])
        XCTAssertEqual(missing.first?.name, "codex")
        XCTAssertTrue(WidgetProviderSelection.providers(from: [], preferences: DisplayPreferences(), selectedIDs: missing.map(\.id)).isEmpty)
        try store.saveSnapshot(Snapshot(fetchedAt: Date(timeIntervalSince1970: 1_721_217_600), stale: false, providers: providers, pollIntervalMinutes: 5))
        let returned = try await query.entities(for: missing.map(\.id))
        XCTAssertEqual(returned.first?.name, "Codex")
        XCTAssertEqual(WidgetProviderSelection.providers(from: providers, preferences: DisplayPreferences(), selectedIDs: returned.map(\.id)).map(\.id), ["codex"])
    }

    func testFamilyLimitsAndOverflowForAllTextScales() {
        let scales: [WidgetCapacityLayout.TextScale] = [.standard, .extraLarge, .accessibility, .largestAccessibility]
        let families: [(WidgetCapacityLayout.Size, [Int])] = [
            (.small, [1, 1, 1, 1]),
            (.medium, [2, 2, 1, 1]),
            (.large, [4, 3, 2, 1]),
        ]
        for (size, limits) in families {
            for (scale, limit) in zip(scales, limits) {
                XCTAssertEqual(WidgetCapacityLayout.providerLimit(size: size, textScale: scale), limit)
                for count in [0, 1, 2, 4, 5] {
                    XCTAssertEqual(WidgetCapacityLayout.overflowCount(providerCount: count, size: size, textScale: scale), max(0, count - limit))
                }
            }
        }
    }

    func testOverflowCountsOnlySelectedVisibleProviders() {
        let selected = WidgetProviderSelection.providers(
            from: providers, preferences: DisplayPreferences(hiddenProviders: ["grok"]),
            selectedIDs: ["missing", "codex", "grok", "cursor", "codex"]
        )
        XCTAssertEqual(WidgetCapacityLayout.overflowCount(providerCount: selected.count, size: .medium, textScale: .standard), 0)
        XCTAssertEqual(WidgetCapacityLayout.overflowCount(providerCount: selected.count, size: .medium, textScale: .accessibility), 1)
    }
}
