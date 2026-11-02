import SwiftUI

/// Maps provider ids to their SVG logo in Assets.xcassets.
enum ProviderLogoAsset {
    static func name(for providerID: String) -> String? {
        switch providerID.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "codex", "codex_cli": "ProviderCodex"
        case "claude", "claude_code": "ProviderClaude"
        case "grok", "xai": "ProviderGrok"
        case "cursor": "ProviderCursor"
        case "copilot", "github_copilot": "ProviderCopilot"
        case "gemini", "gemini_cli": "ProviderGemini"
        default: nil
        }
    }
}

/// Provider logo drawn flat (no tile behind it) so it doesn't add another card layer.
struct ProviderMark: View {
    let providerID: String
    let size: CGFloat

    /// Grows with Dynamic Type (capped) so the mark keeps up with the text beside it.
    @ScaledMetric private var scale: CGFloat = 1

    // Explicit so the private @ScaledMetric doesn't make the memberwise init private.
    init(providerID: String, size: CGFloat) {
        self.providerID = providerID
        self.size = size
    }

    var body: some View {
        let side = CapacityLayout.markSide(base: size, scale: scale)
        Group {
            if let asset = ProviderLogoAsset.name(for: providerID) {
                Image(asset).resizable().scaledToFit()
            } else {
                Image(systemName: "gauge.with.needle")
                    .font(.system(size: side * 0.7, weight: .semibold))
            }
        }
        .frame(width: side, height: side)
        .foregroundStyle(.primary)
        .accessibilityHidden(true)
    }
}

/// Dynamic Type layout rules shared by the dashboard and the widget.
enum CapacityLayout {
    /// Base point size of the dashboard's big "NN%" (scaled with @ScaledMetric relative to .largeTitle).
    static let percentBaseSize: CGFloat = 30

    /// Decorative marks scale with text but stop at 1.5x so they don't crowd out the numbers.
    static let maxMarkScale: CGFloat = 1.5

    static func markSide(base: CGFloat, scale: CGFloat) -> CGFloat {
        base * min(max(scale, 1), maxMarkScale)
    }

    /// The large widget has a fixed height; show fewer rows as text grows so rows aren't clipped.
    static func widgetRowLimit(for size: DynamicTypeSize) -> Int {
        if size.isAccessibilitySize { return 2 }
        if size >= .xxLarge { return 3 }
        return 4
    }

    /// How many provider rows each widget family can hold. Small = 1, medium = 2, large = 4.
    static func widgetCapacity(for family: WidgetFamilyKind) -> Int {
        switch family {
        case .small: 1
        case .medium: 2
        case .large: 4
        }
    }

    /// Rows a widget actually shows: the user's choice (1-4), capped by the family and Dynamic Type.
    static func widgetRowCount(requested: Int, family: WidgetFamilyKind, dynamicType: DynamicTypeSize) -> Int {
        let clamped = min(max(requested, 1), 4)
        return max(1, min(clamped, widgetCapacity(for: family), family == .large ? widgetRowLimit(for: dynamicType) : 4))
    }

    /// At accessibility sizes the dashboard card stacks the percentage under the name.
    static func stacksCardHeader(for size: DynamicTypeSize) -> Bool {
        size.isAccessibilitySize
    }
}

/// WidgetKit-independent family so layout rules are unit-testable from the app target.
enum WidgetFamilyKind: Sendable { case small, medium, large }
