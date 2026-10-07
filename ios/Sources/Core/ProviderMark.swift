import SwiftUI

struct ProviderMark: View {
    let size: CGFloat
    let cornerRadius: CGFloat

    var body: some View {
        Image(systemName: "gauge.with.needle")
            .font(.system(size: size * 0.42, weight: .semibold))
            .frame(width: size, height: size)
            .foregroundStyle(.primary)
            .background(.quaternary, in: RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
            .accessibilityHidden(true)
    }
}
