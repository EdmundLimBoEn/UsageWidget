import SwiftUI

struct DashboardView: View {
    @Environment(AppModel.self) private var model
    @State private var showDiagnostics = false

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 14) {
                freshnessButton

                if model.visibleProviders.isEmpty {
                    ContentUnavailableView(
                        "No usage yet",
                        systemImage: "gauge.with.dots.needle.0percent",
                        description: Text("Refresh after the server collects its first snapshot.")
                    )
                    .frame(minHeight: 360)
                } else {
                    ForEach(model.visibleProviders) { provider in
                        ProviderCapacityCard(provider: provider)
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
        }
        .background(Color(uiColor: .systemGroupedBackground))
        .navigationTitle("Capacity")
        .toolbar {
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
        .refreshable { await model.refresh() }
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
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.tertiary)
            }
            .padding(14)
            .background(Color(uiColor: .secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        }
        .buttonStyle(.plain)
        .accessibilityHint("Shows collection and widget delivery details")
    }

    private var freshnessTitle: String {
        switch model.freshness {
        case .collecting: "Collecting usage"
        case .current: "Usage is current"
        case .stale: "Showing last known usage"
        case .unavailable: "Usage unavailable"
        }
    }

    private var freshnessDetail: String {
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
        }
    }

    private var freshnessTint: Color {
        switch model.freshness {
        case .collecting: .secondary
        case .current: .green
        case .stale: .orange
        case .unavailable: .red
        }
    }
}

struct ProviderCapacityCard: View {
    let provider: Provider

    private var leadingWindow: UsageWindow? { provider.windows.first }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 11) {
                providerMark
                VStack(alignment: .leading, spacing: 1) {
                    Text(provider.name)
                        .font(.headline)
                    if let error = provider.error, !error.isEmpty {
                        Text("Source needs attention")
                            .font(.caption)
                            .foregroundStyle(.red)
                    } else if let credits = provider.credits {
                        Text("\(credits.availableCount) reset credits")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                Spacer()
                if let window = leadingWindow {
                    VStack(alignment: .trailing, spacing: 0) {
                        Text(String(format: "%.0f%%", window.remainingPercent))
                            .font(.system(size: 30, weight: .semibold, design: .rounded).monospacedDigit())
                            .foregroundStyle(capacityTint(window.remainingPercent))
                        Text("remaining")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }

            if let error = provider.error, !error.isEmpty {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.subheadline)
                    .foregroundStyle(.red)
            }

            if provider.windows.isEmpty && provider.error?.isEmpty != false {
                Text("No rate windows reported")
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
        .padding(16)
        .background(Color(uiColor: .secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .accessibilityElement(children: .contain)
    }

    private var providerMark: some View {
        Text(String(provider.name.prefix(1)).uppercased())
            .font(.subheadline.weight(.bold))
            .frame(width: 34, height: 34)
            .foregroundStyle(.primary)
            .background(.quaternary, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
            .accessibilityHidden(true)
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
            HStack {
                Text(String(format: "%.1f%% used", window.usedPercent))
                Spacer()
                Text(String(format: "%.1f%% left", window.remainingPercent))
            }
            .font(.caption2.monospacedDigit())
            .foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityText)
    }

    private var resetText: String {
        guard let reset = window.resetsAt else { return "Reset unknown" }
        return "Resets \(RelativeTime.string(for: reset))"
    }

    private var accessibilityText: String {
        "\(window.title), \(Int(window.remainingPercent)) percent remaining, \(resetText)"
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
                Button("Refresh diagnostics") { Task { await model.refresh() } }
            }
        }
        .navigationTitle("Update health")
    }

    private func relative(_ date: Date?) -> String {
        guard let date else { return "Never" }
        return RelativeTime.string(for: date)
    }
}
