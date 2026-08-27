import SwiftUI

enum ProviderLogoAsset {
    static func name(for providerID: String) -> String? {
        switch providerID.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "codex", "codex_cli":
            "ProviderCodex"
        case "claude", "claude_code":
            "ProviderClaude"
        case "grok", "xai":
            "ProviderGrok"
        case "cursor":
            "ProviderCursor"
        default:
            nil
        }
    }
}

struct ProviderMark: View {
    let providerID: String
    let providerName: String
    let size: CGFloat
    let cornerRadius: CGFloat

    var body: some View {
        Group {
            if let assetName = ProviderLogoAsset.name(for: providerID) {
                Image(assetName)
                    .resizable()
                    .scaledToFit()
                    .padding(size * 0.2)
            } else {
                Text(String(providerName.prefix(1)).uppercased())
                    .font(.system(size: size * 0.38, weight: .bold))
            }
        }
        .frame(width: size, height: size)
        .foregroundStyle(.primary)
        .background(.quaternary, in: RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
        .accessibilityHidden(true)
    }
}
