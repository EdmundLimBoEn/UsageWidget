import WidgetKit
import SwiftUI

struct ProviderEntry: TimelineEntry {
    let date: Date
    let snapshot: Snapshot?
    let preferences: DisplayPreferences
    let fetchError: String?
    var selectedProviderIDs: [String]? = nil
}

struct UsageTimelineProvider: AppIntentTimelineProvider {
    func placeholder(in context: Context) -> ProviderEntry {
        ProviderEntry(date: Date(), snapshot: Self.sampleSnapshot, preferences: DisplayPreferences(), fetchError: nil)
    }

    func snapshot(for configuration: ProviderWidgetIntent, in context: Context) async -> ProviderEntry {
        if context.isPreview {
            let store = SnapshotStore.shared
            return ProviderEntry(
                date: Date(), snapshot: store.loadSnapshot() ?? Self.sampleSnapshot,
                preferences: store.loadPreferences(), fetchError: nil,
                selectedProviderIDs: configuration.selectedIDs
            )
        }
        return await entry(for: configuration)
    }

    func timeline(for configuration: ProviderWidgetIntent, in context: Context) async -> Timeline<ProviderEntry> {
        let entry = await entry(for: configuration)
        let minutes = max(entry.snapshot?.pollIntervalMinutes ?? 5, 1)
        let next = Date().addingTimeInterval(TimeInterval(minutes * 60))
        return Timeline(entries: [entry], policy: .after(next))
    }

    private func entry(for configuration: ProviderWidgetIntent) async -> ProviderEntry {
        var entry = await Self.loadEntry()
        entry.selectedProviderIDs = configuration.selectedIDs
        return entry
    }

    private static func loadEntry() async -> ProviderEntry {
        let store = SnapshotStore.shared
        let prefs = store.loadPreferences()
        let cached = store.loadSnapshot()

        let creds: ConnectionCredentials
        do {
            guard let saved = try KeychainStore.shared.load() else {
                return ProviderEntry(date: Date(), snapshot: cached, preferences: prefs, fetchError: FriendlyError.notSetUp)
            }
            creds = saved
        } catch {
            // e.g. errSecInteractionNotAllowed before first unlock: not the same as "never set up".
            return ProviderEntry(date: Date(), snapshot: cached, preferences: prefs, fetchError: FriendlyError.message(for: error))
        }

        do {
            let client = try APIClient.make(credentials: creds, timeout: 8)
            let snap = try await client.fetchSnapshot()
            try store.saveSnapshot(snap)
            return ProviderEntry(date: Date(), snapshot: snap, preferences: prefs, fetchError: nil)
        } catch {
            var stale = cached
            if stale != nil {
                stale?.stale = true
            }
            return ProviderEntry(date: Date(), snapshot: stale, preferences: prefs, fetchError: FriendlyError.message(for: error))
        }
    }

    static var sampleSnapshot: Snapshot { SampleCapacity.snapshot }
}

struct UsageWidgetPushHandler: WidgetPushHandler {
    func pushTokenDidChange(_ pushInfo: WidgetPushInfo, widgets: [WidgetInfo]) {
        let hex = pushInfo.token.map { String(format: "%02x", $0) }.joined()
        SnapshotStore.shared.setPendingWidgetToken(hex)
    }
}

struct ProviderUsageWidget: Widget {
    let kind = "ProviderUsageWidget"

    var body: some WidgetConfiguration {
        AppIntentConfiguration(kind: kind, intent: ProviderWidgetIntent.self, provider: UsageTimelineProvider()) { entry in
            ProviderWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Capacity")
        .description("Choose providers and a small, medium, or large Home Screen size.")
        .supportedFamilies([.systemSmall, .systemMedium, .systemLarge])
        .pushHandler(UsageWidgetPushHandler.self)
    }
}

struct ProviderWidgetView: View {
    let entry: ProviderEntry

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @Environment(\.widgetFamily) private var family

    private var size: WidgetCapacityLayout.Size {
        switch family {
        case .systemSmall: return .small
        case .systemMedium: return .medium
        default: return .large
        }
    }

    private var textScale: WidgetCapacityLayout.TextScale {
        if dynamicTypeSize >= .accessibility3 { return .largestAccessibility }
        if dynamicTypeSize.isAccessibilitySize { return .accessibility }
        if dynamicTypeSize >= .xxLarge { return .extraLarge }
        return .standard
    }

    private var maxRows: Int { WidgetCapacityLayout.providerLimit(size: size, textScale: textScale) }

    private var emptyMessage: String {
        if entry.selectedProviderIDs?.isEmpty == true { return "Edit Widget to choose providers" }
        if let error = entry.fetchError { return error }
        if entry.selectedProviderIDs != nil { return "Selected providers unavailable" }
        return "No providers"
    }

    var body: some View {
        let visible = WidgetProviderSelection.providers(
            from: entry.snapshot?.providers ?? [],
            preferences: entry.preferences,
            selectedIDs: entry.selectedProviderIDs
        )
        let overflow = WidgetCapacityLayout.overflowCount(providerCount: visible.count, size: size, textScale: textScale)
        let shown = Array(visible.prefix(maxRows))

        VStack(alignment: .leading, spacing: family == .systemLarge ? 10 : 6) {
            if family == .systemLarge || (family == .systemMedium && !dynamicTypeSize.isAccessibilitySize) {
                HStack {
                    Text("Capacity")
                        .font(.headline)
                    Spacer()
                    ageLabel
                }
            }

            if shown.isEmpty {
                Spacer(minLength: 0)
                Text(emptyMessage)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer(minLength: 0)
            } else {
                if family == .systemLarge {
                    ForEach(shown) { provider in
                        if dynamicTypeSize.isAccessibilitySize {
                            CompactProviderWidgetView(
                                provider: provider,
                                isStale: entry.snapshot?.stale == true || provider.stale
                            )
                        } else {
                            ProviderWidgetRow(provider: provider, isStale: entry.snapshot?.stale == true || provider.stale)
                        }
                    }
                } else {
                    HStack(alignment: .top, spacing: 12) {
                        ForEach(shown) { provider in
                            CompactProviderWidgetView(
                                provider: provider,
                                isStale: entry.snapshot?.stale == true || provider.stale
                            )
                        }
                    }
                }
                Spacer(minLength: 0)
            }

            if overflow > 0 || (family == .systemSmall && !dynamicTypeSize.isAccessibilitySize) {
                HStack {
                    if family == .systemSmall && !dynamicTypeSize.isAccessibilitySize {
                        ageLabel
                    }
                    if overflow > 0 {
                        Text("+\(overflow) more")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .layoutPriority(1)
                            .accessibilityLabel("\(overflow) more providers. Choose a larger widget or edit the provider selection.")
                    }
                }
            }
        }
        .padding(2)
    }

    @ViewBuilder
    private var ageLabel: some View {
        let text: String = {
            if let fetched = entry.snapshot?.fetchedAt {
                return RelativeTime.string(for: fetched)
            }
            return "—"
        }()
        HStack(spacing: 5) {
            if entry.snapshot?.stale == true {
                Image(systemName: "clock.badge.exclamationmark")
                    .foregroundStyle(.orange)
                    .font(.caption2)
            } else {
                Image(systemName: "checkmark.circle.fill")
                    .foregroundStyle(.green)
                    .font(.caption2)
            }
            Text(text)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .accessibilityLabel(entry.snapshot?.stale == true ? "Stale, updated \(text)" : "Updated \(text)")
    }
}

private struct CompactProviderWidgetView: View {
    let provider: Provider
    let isStale: Bool

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    private var primary: UsageWindow? { provider.windows.first }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 4) {
                Text(provider.name)
                    .font(.caption2.weight(.semibold))
                    .lineLimit(1)
                if isStale {
                    Image(systemName: "clock.badge.exclamationmark")
                        .font(.caption2)
                        .foregroundStyle(.orange)
                }
            }
            if let primary {
                Text(String(format: dynamicTypeSize.isAccessibilitySize ? "%.0f%%" : "%.0f%% left", primary.remainingPercent))
                    .font(dynamicTypeSize.isAccessibilitySize ? .headline : .title2.weight(.semibold))
                    .monospacedDigit()
                    .foregroundStyle(widgetCapacityTint(primary.remainingPercent, stale: isStale))
                    .lineLimit(1)
                if !dynamicTypeSize.isAccessibilitySize {
                    Text(ForecastText.string(for: primary) ?? resetText(primary))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                    ProgressView(value: min(max(primary.usedPercent / 100, 0), 1))
                        .tint(widgetCapacityTint(primary.remainingPercent, stale: isStale))
                }
            } else {
                Text(errorText)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(dynamicTypeSize.isAccessibilitySize ? 1 : 2)
            }
        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityText)
    }

    private var errorText: String {
        guard let error = provider.error, !error.isEmpty else { return "No usage data" }
        return error
    }

    private var accessibilityText: String {
        var parts = [provider.name]
        if isStale { parts.append("Stale") }
        if let primary {
            parts.append(String(format: "%.0f percent remaining", primary.remainingPercent))
            parts.append(resetText(primary))
            if let forecast = ForecastText.string(for: primary) { parts.append(forecast) }
        } else {
            parts.append(errorText)
        }
        return parts.joined(separator: ", ")
    }

    private func resetText(_ window: UsageWindow) -> String {
        guard let reset = window.resetsAt else { return window.title }
        return "Resets \(RelativeTime.string(for: reset))"
    }
}

struct ProviderWidgetRow: View {
    let provider: Provider
    let isStale: Bool

    private var primary: UsageWindow? { provider.windows.first }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                ProviderMark(size: 24, cornerRadius: 7)
                VStack(alignment: .leading, spacing: 0) {
                    Text(provider.name)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    if let primary {
                        Text(ForecastText.string(for: primary) ?? resetText(primary))
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                Spacer(minLength: 8)
                if let primary {
                    VStack(alignment: .trailing, spacing: 0) {
                        Text(String(format: "%.0f%%", primary.remainingPercent))
                            .font(.title3.weight(.semibold).monospacedDigit())
                            .foregroundStyle(widgetCapacityTint(primary.remainingPercent, stale: isStale))
                        Text("left")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            if let primary {
                ProgressView(value: min(max(primary.usedPercent / 100, 0), 1))
                    .tint(widgetCapacityTint(primary.remainingPercent, stale: isStale))
            } else if let err = provider.error, !err.isEmpty {
                Text(err)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(1)
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .background(.quaternary, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(accessibilityText)
    }

    private var accessibilityText: String {
        var parts = [provider.name]
        if let primary {
            parts.append(String(format: "primary %.0f percent used, %.0f remaining", primary.usedPercent, primary.remainingPercent))
        }
        if let reset = primary?.resetsAt {
            parts.append("resets \(RelativeTime.string(for: reset))")
        }
        if let primary, let forecast = ForecastText.string(for: primary) { parts.append(forecast) }
        return parts.joined(separator: ", ")
    }

    private func resetText(_ window: UsageWindow) -> String {
        guard let reset = window.resetsAt else { return window.title }
        return "\(window.title) · resets \(RelativeTime.string(for: reset))"
    }
}

private func widgetCapacityTint(_ remaining: Double, stale: Bool) -> Color {
    if stale { return .secondary }
    if remaining <= 10 { return .red }
    if remaining <= 25 { return .orange }
    return .accentColor
}

struct OverflowRow: View {
    let count: Int

    var body: some View {
        Text("+\(count) more")
            .font(.subheadline.weight(.medium))
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityLabel("\(count) more providers")
    }
}
