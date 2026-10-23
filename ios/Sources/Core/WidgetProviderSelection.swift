import Foundation

/// Nil selection follows app preferences, including widgets created before configuration existed.
public enum WidgetProviderSelection {
    public static func providers(
        from providers: [Provider],
        preferences: DisplayPreferences,
        selectedIDs: [String]? = nil
    ) -> [Provider] {
        let visible = ProviderDisplay.orderedVisible(
            providers: providers,
            order: preferences.providerOrder,
            hidden: preferences.hiddenSet
        )
        var seen = Set<String>()
        let unique = visible.filter { seen.insert($0.id).inserted }
        guard let selectedIDs else { return unique }

        let byID = Dictionary(uniqueKeysWithValues: unique.map { ($0.id, $0) })
        seen.removeAll()
        return selectedIDs.compactMap { id in
            guard seen.insert(id).inserted else { return nil }
            return byID[id]
        }
    }
}

public enum WidgetCapacityLayout {
    public enum Size: Equatable, Sendable { case small, medium, large }
    public enum TextScale: Equatable, Sendable { case standard, extraLarge, accessibility, largestAccessibility }

    public static func providerLimit(size: Size, textScale: TextScale) -> Int {
        switch size {
        case .small: return 1
        case .medium:
            return textScale == .accessibility || textScale == .largestAccessibility ? 1 : 2
        case .large:
            switch textScale {
            case .standard: return 4
            case .extraLarge: return 3
            case .accessibility: return 2
            case .largestAccessibility: return 1
            }
        }
    }

    public static func overflowCount(providerCount: Int, size: Size, textScale: TextScale) -> Int {
        max(0, providerCount - providerLimit(size: size, textScale: textScale))
    }
}
