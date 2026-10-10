import SwiftUI

struct DashboardView: View {
    @Environment(AppModel.self) private var model
    @State private var showDiagnostics = false

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 14) {
                if model.homeSurface == .samplePreview {
                    sampleBanner
                }

                freshnessButton

                if model.visibleProviders.isEmpty {
                    if model.snapshot != nil {
                        ContentUnavailableView(
                            "No providers to show",
                            systemImage: "eye.slash",
                            description: Text("Providers are hidden or not in this collection.")
                        )
                        .frame(minHeight: 360)
                    } else {
                        ContentUnavailableView(
                            "No usage yet",
                            systemImage: "gauge.with.dots.needle.0percent",
                            description: Text("Refresh after the server collects its first snapshot.")
                        )
                        .frame(minHeight: 360)
                    }
                } else {
                    ForEach(model.visibleProviders) { provider in
                        ProviderCapacityRow(provider: provider)
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
        }
        .background(Color(uiColor: .systemGroupedBackground))
        .navigationTitle("Capacity")
        .toolbar {
            if model.homeSurface != .samplePreview {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        Task { await model.refresh() }
                    } label: {
                        if model.isLoading {
                            ProgressView()
                        } else {
                            Image(systemName: "arrow.clockwise")
                        }
                    }
                    .disabled(model.isLoading)
                    .accessibilityLabel("Refresh usage")
                }
            }
        }
        .refreshable {
            guard model.homeSurface != .samplePreview else { return }
            await model.refresh()
        }
        .sheet(isPresented: $showDiagnostics) {
            NavigationStack {
                HealthDiagnosticsView()
                    .toolbar {
                        ToolbarItem(placement: .confirmationAction) {
                            Button("Done") { showDiagnostics = false }
                        }
                    }
            }
        }
    }

    private var sampleBanner: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label("Sample data", systemImage: "eye")
                .font(.subheadline.weight(.semibold))
            Text("This dashboard shows example capacity so you can try the app before connecting a server. It is not your usage.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(Color(uiColor: .secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Sample data. This dashboard shows example capacity, not your usage.")
    }

    private var freshnessButton: some View {
        Button {
            showDiagnostics = true
        } label: {
            HStack(spacing: 10) {
                Image(systemName: freshnessIcon)
                    .foregroundStyle(freshnessTint)
                VStack(alignment: .leading, spacing: 2) {
                    Text(freshnessTitle)
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(.primary)
                    Text(freshnessDetail)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                Spacer()
                if model.homeSurface != .samplePreview {
                    Image(systemName: "chevron.right")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.tertiary)
                }
            }
            .padding(14)
            .background(Color(uiColor: .secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        }
        .buttonStyle(.plain)
        .disabled(model.homeSurface == .samplePreview)
        .accessibilityHint(model.homeSurface == .samplePreview ? "Sample data is not collected from a server" : "Shows collection and widget delivery details")
    }

    private var freshnessTitle: String {
        switch model.freshness {
        case .collecting: "Collecting usage"
        case .current: "Usage is current"
        case .stale: "Showing last known usage"
        case .unavailable: "Usage unavailable"
        case .sample: "Sample data"
        }
    }

    private var freshnessDetail: String {
        if model.homeSurface == .samplePreview {
            return "Example remaining capacity and reset windows"
        }
        if let detail = model.health?.collector?.lastError, model.freshness != .current {
            return detail
        }
        return model.dataAgeText
    }

    private var freshnessIcon: String {
        switch model.freshness {
        case .collecting: "arrow.trianglehead.2.clockwise.rotate.90"
        case .current: "checkmark.circle.fill"
        case .stale: "clock.badge.exclamationmark"
        case .unavailable: "exclamationmark.circle.fill"
        case .sample: "eye"
        }
    }

    private var freshnessTint: Color {
        switch model.freshness {
        case .collecting: .secondary
        case .current: .green
        case .stale: .orange
        case .unavailable: .red
        case .sample: .secondary
        }
    }
}

struct ProviderCapacityRow: View {
    let provider: Provider

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

    private var leadingWindow: UsageWindow? { provider.windows.first }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            if CapacityLayout.stacksCardHeader(for: dynamicTypeSize) {
                VStack(alignment: .leading, spacing: 8) {
                    HStack(spacing: 11) {
                        ProviderMark(size: 34, cornerRadius: 9)
                        nameBlock
                    }
                    if let window = leadingWindow {
                        percentBlock(window, alignment: .leading)
                    }
                }
            } else {
                HStack(spacing: 11) {
                    ProviderMark(size: 34, cornerRadius: 9)
                    nameBlock
                    Spacer()
                    if let window = leadingWindow {
                        percentBlock(window, alignment: .trailing)
                    }
                }
            }

            if let error = provider.error, !error.isEmpty,
               !provider.stale || provider.windows.isEmpty {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.subheadline)
                    .foregroundStyle(.red)
            }

            if provider.windows.isEmpty && provider.error?.isEmpty != false {
                Text("No usage limits reported")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            } else {
                VStack(spacing: 13) {
                    ForEach(provider.windows) { window in
                        CapacityWindowRow(window: window)
                    }
                }
            }
        }
        .padding(.vertical, 12)
        .accessibilityElement(children: .contain)
    }

    private var nameBlock: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(provider.name)
                .font(.headline)
            if provider.stale && !provider.windows.isEmpty {
                Text("Showing last known usage")
                    .font(.caption)
                    .foregroundStyle(.orange)
            } else if let error = provider.error, !error.isEmpty {
                Text("Source needs attention")
                    .font(.caption)
                    .foregroundStyle(.red)
            } else if let credits = provider.credits {
                Text("\(credits.availableCount) reset credits")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func percentBlock(_ window: UsageWindow, alignment: HorizontalAlignment) -> some View {
        VStack(alignment: alignment, spacing: 0) {
            CapacityPercentText(remaining: window.remainingPercent)
            Text("remaining")
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
    }
}

/// The big "NN%" on each dashboard provider row. Sized with @ScaledMetric so it follows
/// Dynamic Type (it used to be a fixed 30pt that ignored the user's text size).
struct CapacityPercentText: View {
    let remaining: Double

    @ScaledMetric(relativeTo: .largeTitle) private var size: CGFloat = CapacityLayout.percentBaseSize

    init(remaining: Double) {
        self.remaining = remaining
    }

    var body: some View {
        Text(String(format: "%.0f%%", remaining))
            .font(.system(size: size, weight: .semibold, design: .rounded).monospacedDigit())
            .lineLimit(1)
            .minimumScaleFactor(0.6)
            .foregroundStyle(capacityTint(remaining))
    }
}

struct CapacityWindowRow: View {
    let window: UsageWindow

    var body: some View {
        VStack(spacing: 7) {
            HStack(alignment: .firstTextBaseline) {
                Text(window.title)
                    .font(.subheadline.weight(.medium))
                Spacer()
                Text(resetText)
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            ProgressView(value: min(max(window.usedPercent / 100, 0), 1))
                .tint(capacityTint(window.remainingPercent))
            if let forecast = ForecastText.string(for: window) {
                Label(forecast, systemImage: window.forecast?.exhaustsBeforeReset == true ? "hourglass.bottomhalf.filled" : "checkmark.circle")
                    .font(.caption2)
                    .foregroundStyle(window.forecast?.exhaustsBeforeReset == true ? .orange : .green)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityText)
    }

    private var resetText: String {
        guard let reset = window.resetsAt else { return "Reset unknown" }
        return "Resets \(RelativeTime.string(for: reset))"
    }

    private var accessibilityText: String {
        ["\(window.title), \(Int(window.remainingPercent)) percent remaining", resetText, ForecastText.string(for: window)].compactMap { $0 }.joined(separator: ", ")
    }
}

private func capacityTint(_ remaining: Double) -> Color {
    if remaining <= 10 { return .red }
    if remaining <= 25 { return .orange }
    return .accentColor
}

struct HealthDiagnosticsView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        List {
            Section("Collection") {
                LabeledContent("Status", value: model.health?.collector?.status ?? "starting")
                LabeledContent("Source", value: model.health?.collector?.source ?? "unknown")
                LabeledContent("Last attempt", value: relative(model.health?.collector?.lastAttemptAt))
                LabeledContent("Last success", value: relative(model.health?.collector?.lastSuccessAt))
                LabeledContent("Next attempt", value: relative(model.health?.collector?.nextAttemptAt))
                if let failures = model.health?.collector?.consecutiveFailures, failures > 0 {
                    LabeledContent("Consecutive failures", value: "\(failures)")
                }
                if let error = model.health?.collector?.lastError, !error.isEmpty {
                    Text(error).font(.footnote).foregroundStyle(.red)
                }
            }
            Section("Widget delivery") {
                LabeledContent("Status", value: model.health?.widgetDelivery?.status ?? "not attempted")
                if let delivery = model.health?.widgetDelivery {
                    LabeledContent("Accepted", value: "\(delivery.succeeded) of \(delivery.attempted)")
                    LabeledContent("Last attempt", value: relative(delivery.lastAttemptAt))
                    if let error = delivery.lastError, !error.isEmpty {
                        Text(error).font(.footnote).foregroundStyle(.orange)
                    }
                }
            }
            Section {
                Button("Refresh") { Task { await model.refresh() } }
            }
        }
        .navigationTitle("Collection")
    }

    private func relative(_ date: Date?) -> String {
        guard let date else { return "Never" }
        return RelativeTime.string(for: date)
    }
}
