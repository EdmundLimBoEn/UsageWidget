import XCTest
import UIKit
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
