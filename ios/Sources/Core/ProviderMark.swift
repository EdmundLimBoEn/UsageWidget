import SwiftUI

struct ProviderMark: View {
    let size: CGFloat
    let cornerRadius: CGFloat

    /// Grows with Dynamic Type (capped) so the mark keeps up with the text beside it.
    @ScaledMetric private var scale: CGFloat = 1

    // Explicit so the private @ScaledMetric doesn't make the memberwise init private.
    init(size: CGFloat, cornerRadius: CGFloat) {
        self.size = size
        self.cornerRadius = cornerRadius
    }

    var body: some View {
        let side = CapacityLayout.markSide(base: size, scale: scale)
        Image(systemName: "gauge.with.needle")
            .font(.system(size: side * 0.42, weight: .semibold))
            .frame(width: side, height: side)
            .foregroundStyle(.primary)
            .background(.quaternary, in: RoundedRectangle(cornerRadius: cornerRadius * side / size, style: .continuous))
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

    /// At accessibility sizes the dashboard card stacks the percentage under the name.
    static func stacksCardHeader(for size: DynamicTypeSize) -> Bool {
        size.isAccessibilitySize
    }
}
