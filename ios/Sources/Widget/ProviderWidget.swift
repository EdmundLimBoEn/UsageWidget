import WidgetKit
import SwiftUI

struct ProviderEntry: TimelineEntry {
    let date: Date
    let snapshot: Snapshot?
    let preferences: DisplayPreferences
    let fetchError: String?
}

struct UsageTimelineProvider: TimelineProvider {
    func placeholder(in context: Context) -> ProviderEntry {
        ProviderEntry(date: Date(), snapshot: Self.sampleSnapshot, preferences: DisplayPreferences(), fetchError: nil)
    }

    func getSnapshot(in context: Context, completion: @escaping (ProviderEntry) -> Void) {
        let finish = UncheckedBox(completion)
        Task {
            finish.value(await Self.loadEntry())
        }
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<ProviderEntry>) -> Void) {
        let finish = UncheckedBox(completion)
        Task {
            let entry = await Self.loadEntry()
            let minutes = max(entry.snapshot?.pollIntervalMinutes ?? 5, 1)
            let next = Date().addingTimeInterval(TimeInterval(minutes * 60))
            finish.value(Timeline(entries: [entry], policy: .after(next)))
        }
    }

    private static func loadEntry() async -> ProviderEntry {
        let store = SnapshotStore.shared
        let prefs = store.loadPreferences()
        let cached = store.loadSnapshot()

        guard let creds = store.mirroredCredentials() ?? (try? KeychainStore.shared.load()) else {
            return ProviderEntry(date: Date(), snapshot: cached, preferences: prefs, fetchError: "Not configured")
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
            return ProviderEntry(date: Date(), snapshot: stale, preferences: prefs, fetchError: String(describing: error))
        }
    }

    static var sampleSnapshot: Snapshot {
        Snapshot(
            fetchedAt: Date().addingTimeInterval(-120),
            stale: false,
            providers: [
                Provider(id: "codex", name: "Codex", windows: [
                    UsageWindow(id: "codex.primary", key: "primary", title: "5h", usedPercent: 42, remainingPercent: 58),
                    UsageWindow(id: "codex.secondary", key: "secondary", title: "Weekly", usedPercent: 11, remainingPercent: 89),
                ]),
                Provider(id: "claude", name: "Claude", windows: [
                    UsageWindow(id: "claude.primary", key: "primary", title: "Session", usedPercent: 20, remainingPercent: 80),
                ]),
                Provider(id: "grok", name: "Grok", windows: [
                    UsageWindow(id: "grok.primary", key: "primary", title: "Rate", usedPercent: 5, remainingPercent: 95),
                ]),
            ],
            pollIntervalMinutes: 5
        )
    }
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
        StaticConfiguration(kind: kind, provider: UsageTimelineProvider()) { entry in
            ProviderWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Usage")
        .description("Remaining AI capacity, reset horizons, and update freshness.")
        .supportedFamilies([.systemLarge])
        .pushHandler(UsageWidgetPushHandler.self)
    }
}

struct ProviderWidgetView: View {
    let entry: ProviderEntry

    private let maxRows = 4

    var body: some View {
        let visible = ProviderDisplay.orderedVisible(
            providers: entry.snapshot?.providers ?? [],
            order: entry.preferences.providerOrder,
            hidden: entry.preferences.hiddenSet
        )
        let overflow = max(0, visible.count - maxRows)
        let shown = Array(visible.prefix(maxRows))

        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Capacity")
                    .font(.headline)
                Spacer()
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
                    ProviderWidgetRow(provider: provider, isStale: entry.snapshot?.stale == true)
                }
                if overflow > 0 {
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

    private var primary: UsageWindow? { provider.windows.first }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(String(provider.name.prefix(1)).uppercased())
                    .font(.caption2.weight(.bold))
                    .frame(width: 24, height: 24)
                    .background(.quaternary, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 0) {
                    Text(provider.name)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    if let primary {
                        Text(resetText(primary))
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
