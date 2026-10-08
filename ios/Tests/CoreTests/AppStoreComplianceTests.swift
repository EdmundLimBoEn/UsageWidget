import XCTest
import UIKit
import SwiftUI
@testable import UsageWidget

final class AppStoreComplianceTests: XCTestCase {
    func testPrivacyManifestDeclaresAppGroupUserDefaultsReason() throws {
        let url = try XCTUnwrap(
            Bundle.main.url(forResource: "PrivacyInfo", withExtension: "xcprivacy"),
            "PrivacyInfo.xcprivacy must ship in the app bundle"
        )
        let data = try Data(contentsOf: url)
        var format = PropertyListSerialization.PropertyListFormat.xml
        let plist = try PropertyListSerialization.propertyList(from: data, options: [], format: &format)
        let root = try XCTUnwrap(plist as? [String: Any])
        XCTAssertEqual(root["NSPrivacyTracking"] as? Bool, false)
        let collected = try XCTUnwrap(root["NSPrivacyCollectedDataTypes"] as? [Any])
        XCTAssertTrue(collected.isEmpty, "collected data types must stay empty; the developer does not receive usage")
        let apiTypes = try XCTUnwrap(root["NSPrivacyAccessedAPITypes"] as? [[String: Any]])
        let userDefaults = try XCTUnwrap(
            apiTypes.first {
                ($0["NSPrivacyAccessedAPIType"] as? String) == "NSPrivacyAccessedAPICategoryUserDefaults"
            }
        )
        let reasons = try XCTUnwrap(userDefaults["NSPrivacyAccessedAPITypeReasons"] as? [String])
        XCTAssertTrue(
            reasons.contains("1C8F.1"),
            "App Group UserDefaults must declare 1C8F.1, found \(reasons)"
        )
        XCTAssertTrue(
            reasons.contains("CA92.1"),
            "UserDefaults.standard fallback must declare CA92.1, found \(reasons)"
        )
    }

    func testProviderImagesetsDoNotShipBrandLogos() {
        for name in ["ProviderCodex", "ProviderClaude", "ProviderGrok", "ProviderCursor"] {
            XCTAssertNil(UIImage(named: name), "\(name) must not remain as a brand logo asset")
        }
    }
}

@MainActor
final class SamplePreviewTests: XCTestCase {
    func testSamplePreviewRendersNonEmptyProviders() {
        let model = AppModel(
            keychain: KeychainStore(service: "usagewidget.tests.\(UUID().uuidString)", accessGroup: nil),
            store: SnapshotStore.temporary()
        )
        XCTAssertEqual(model.homeSurface, .setup)
        XCTAssertTrue(model.visibleProviders.isEmpty)
        model.enterSamplePreview()
        XCTAssertEqual(model.homeSurface, .samplePreview)
        XCTAssertEqual(model.visibleProviders.map(\.id), ["cursor", "codex", "claude_code"])
        XCTAssertEqual(model.visibleProviders[0].windows[0].usedPercent, 45, accuracy: 0.001)
        XCTAssertEqual(model.visibleProviders[0].windows[0].remainingPercent, 55, accuracy: 0.001)
        XCTAssertEqual(model.visibleProviders[1].windows[0].usedPercent, 42, accuracy: 0.001)
        XCTAssertEqual(model.freshness, .sample)
        XCTAssertEqual(model.snapshot?.sourceKind, "sample")
        XCTAssertFalse(model.isConfigured)
    }
}

@MainActor
final class DynamicTypeTests: XCTestCase {
    private func renderedHeight<V: View>(_ view: V, at size: DynamicTypeSize) -> CGFloat {
        let host = UIHostingController(rootView: view.environment(\.dynamicTypeSize, size))
        return host.sizeThatFits(in: CGSize(width: CGFloat.greatestFiniteMagnitude, height: .greatestFiniteMagnitude)).height
    }

    func testDashboardPercentageGrowsWithDynamicType() {
        let label = CapacityPercentText(remaining: 58)
        let standard = renderedHeight(label, at: .large)
        let xxxLarge = renderedHeight(label, at: .xxxLarge)
        let accessibility = renderedHeight(label, at: .accessibility5)
        XCTAssertGreaterThan(standard, 20, "sanity: a 30pt label renders taller than 20pt")
        XCTAssertGreaterThan(xxxLarge, standard * 1.1, "percent label ignores Dynamic Type (\(standard) -> \(xxxLarge))")
        XCTAssertGreaterThan(accessibility, standard * 1.4, "percent label ignores accessibility sizes (\(standard) -> \(accessibility))")
    }

    func testFixedSizeFontWouldFailTheSameCheck() {
        // Guards the test above: a fixed-size font must NOT pass it.
        let fixed = Text("58%").font(.system(size: 30, weight: .semibold, design: .rounded))
        let standard = renderedHeight(fixed, at: .large)
        let accessibility = renderedHeight(fixed, at: .accessibility5)
        XCTAssertEqual(accessibility, standard, accuracy: 1)
    }

    func testWidgetShowsFewerRowsAsTextGrows() {
        XCTAssertEqual(CapacityLayout.widgetRowLimit(for: .large), 4)
        XCTAssertEqual(CapacityLayout.widgetRowLimit(for: .xLarge), 4)
        XCTAssertEqual(CapacityLayout.widgetRowLimit(for: .xxLarge), 3)
        XCTAssertEqual(CapacityLayout.widgetRowLimit(for: .xxxLarge), 3)
        XCTAssertEqual(CapacityLayout.widgetRowLimit(for: .accessibility1), 2)
        XCTAssertEqual(CapacityLayout.widgetRowLimit(for: .accessibility5), 2)
    }

    func testCardHeaderStacksOnlyAtAccessibilitySizes() {
        XCTAssertFalse(CapacityLayout.stacksCardHeader(for: .xxxLarge))
        XCTAssertTrue(CapacityLayout.stacksCardHeader(for: .accessibility1))
    }

    func testProviderMarkScalesButIsCapped() {
        XCTAssertEqual(CapacityLayout.markSide(base: 34, scale: 1), 34)
        XCTAssertEqual(CapacityLayout.markSide(base: 34, scale: 0.8), 34, "never shrinks below the design size")
        XCTAssertEqual(CapacityLayout.markSide(base: 34, scale: 1.2), 34 * 1.2, accuracy: 0.001)
        XCTAssertEqual(CapacityLayout.markSide(base: 34, scale: 3), 51, "capped at 1.5x")
    }
}
