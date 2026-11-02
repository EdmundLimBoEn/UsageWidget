import WidgetKit
import SwiftUI
import AppIntents

/// Widget configuration: how many providers to show (1-4). The family caps it
/// (small fits 1, medium 2, large 4), so users size the widget down for fewer.
struct CapacityWidgetIntent: WidgetConfigurationIntent {
    static let title: LocalizedStringResource = "Capacity"
    static let description = IntentDescription("Choose how many providers the widget shows.")

    @Parameter(title: "Providers shown", default: 4, inclusiveRange: (1, 4))
    var providerCount: Int

    init() {}
    init(providerCount: Int) { self.providerCount = providerCount }
}

struct ProviderEntry: TimelineEntry {
    let date: Date
    let snapshot: Snapshot?
    let preferences: DisplayPreferences
    let fetchError: String?
    var providerCount: Int = 4
}

struct UsageTimelineProvider: AppIntentTimelineProvider {
    func placeholder(in context: Context) -> ProviderEntry {
        ProviderEntry(date: Date(), snapshot: Self.sampleSnapshot, preferences: DisplayPreferences(), fetchError: nil)
    }

    func snapshot(for configuration: CapacityWidgetIntent, in context: Context) async -> ProviderEntry {
        if context.isPreview {
            return ProviderEntry(date: Date(), snapshot: Self.sampleSnapshot, preferences: DisplayPreferences(), fetchError: nil, providerCount: configuration.providerCount)
        }
        var entry = await Self.loadEntry()
        entry.providerCount = configuration.providerCount
        return entry
    }

    func timeline(for configuration: CapacityWidgetIntent, in context: Context) async -> Timeline<ProviderEntry> {
        var entry = await Self.loadEntry()
        entry.providerCount = configuration.providerCount
        let minutes = max(entry.snapshot?.pollIntervalMinutes ?? 5, 1)
        let next = Date().addingTimeInterval(TimeInterval(minutes * 60))
        return Timeline(entries: [entry], policy: .after(next))
    }

    private static func loadEntry() async -> ProviderEntry {
        let store = SnapshotStore.shared

        let creds: ConnectionCredentials
        do {
            guard let saved = try KeychainStore.shared.load() else {
                return ProviderEntry(date: Date(), snapshot: nil, preferences: DisplayPreferences(), fetchError: FriendlyError.notSetUp)
            }
            creds = saved
        } catch {
            // e.g. errSecInteractionNotAllowed before first unlock: not the same as "never set up".
            return ProviderEntry(date: Date(), snapshot: nil, preferences: DisplayPreferences(), fetchError: FriendlyError.message(for: error))
        }

        let prefs = store.loadPreferences(connection: creds)
        let cached = store.loadSnapshot(connection: creds)
        do {
            let client = try APIClient.make(credentials: creds, timeout: 8)
            let snap = try await client.fetchSnapshot()
            guard (try? KeychainStore.shared.load()) == creds else {
                return ProviderEntry(date: Date(), snapshot: nil, preferences: DisplayPreferences(), fetchError: nil)
            }
            try store.saveSnapshot(snap, connection: creds)
            return ProviderEntry(date: Date(), snapshot: snap, preferences: prefs, fetchError: nil)
        } catch {
            guard (try? KeychainStore.shared.load()) == creds else {
                return ProviderEntry(date: Date(), snapshot: nil, preferences: DisplayPreferences(), fetchError: nil)
            }
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
        AppIntentConfiguration(kind: kind, intent: CapacityWidgetIntent.self, provider: UsageTimelineProvider()) { entry in
            ProviderWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Capacity")
        .description("Remaining AI capacity and when it resets.")
        .supportedFamilies([.systemSmall, .systemMedium, .systemLarge])
        .pushHandler(UsageWidgetPushHandler.self)
    }
}

struct ProviderWidgetView: View {
    let entry: ProviderEntry

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @Environment(\.widgetFamily) private var widgetFamily

    private var familyKind: WidgetFamilyKind {
        switch widgetFamily {
        case .systemSmall: .small
        case .systemMedium: .medium
        default: .large
        }
    }

    private var maxRows: Int {
        CapacityLayout.widgetRowCount(requested: entry.providerCount, family: familyKind, dynamicType: dynamicTypeSize)
    }

    var body: some View {
        let visible = ProviderDisplay.orderedVisible(
            providers: entry.snapshot?.providers ?? [],
            order: entry.preferences.providerOrder,
            hidden: entry.preferences.hiddenSet
        )
        // Only flag overflow when the family or text size cut the user's choice short.
        let overflow = entry.providerCount > maxRows ? max(0, visible.count - maxRows) : 0
        let shown = Array(visible.prefix(maxRows))

        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Capacity")
                    .font(familyKind == .small ? .caption.weight(.semibold) : .headline)
                Spacer()
                if overflow > 0 && familyKind != .large {
                    Text("+\(overflow)")
                        .font(.caption2.weight(.medium))
                        .foregroundStyle(.secondary)
                        .accessibilityLabel("\(overflow) more providers")
                }
                ageLabel
            }

            if shown.isEmpty {
                Spacer()
                Text(entry.fetchError ?? "No providers")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                Spacer()
            } else {
                ForEach(shown) { provider in
                    ProviderWidgetRow(provider: provider, isStale: entry.snapshot?.stale == true, compact: familyKind == .small)
                }
                // Small/medium have no room for an extra row; the header carries the count instead.
                if overflow > 0 && familyKind == .large {
                    OverflowRow(count: overflow)
                }
                Spacer(minLength: 0)
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
        }
        .accessibilityLabel(entry.snapshot?.stale == true ? "Stale, updated \(text)" : "Updated \(text)")
    }
}

struct ProviderWidgetRow: View {
    let provider: Provider
    let isStale: Bool
    var compact: Bool = false

    private var primary: UsageWindow? { provider.windows.first }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                ProviderMark(providerID: provider.id, size: compact ? 18 : 22)
                VStack(alignment: .leading, spacing: 0) {
                    Text(provider.name)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    if let primary, !compact {
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

/// Bridges WidgetKit completion handlers into Swift 6 tasks without data-race diagnostics.
private struct UncheckedBox<T>: @unchecked Sendable {
    let value: T
    init(_ value: T) { self.value = value }
}
