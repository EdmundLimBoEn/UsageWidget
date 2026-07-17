import SwiftUI

struct DashboardView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        List {
            statusSection
            if let providers = model.snapshot?.providers, !providers.isEmpty {
                ForEach(orderedProviders(providers)) { provider in
                    Section {
                        ProviderSection(provider: provider)
                    } header: {
                        HStack {
                            Text(provider.name)
                            if model.preferences.hiddenSet.contains(provider.id) {
                                Text("Hidden")
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            } else {
                ContentUnavailableView(
                    "No usage data",
                    systemImage: "antenna.radiowaves.left.and.right",
                    description: Text("Pull to refresh once the server has polled CodexBar.")
                )
            }
        }
        .navigationTitle("Usage")
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                if model.isLoading {
                    ProgressView()
                } else {
                    Button {
                        Task { await model.refresh() }
                    } label: {
                        Image(systemName: "arrow.clockwise")
                    }
                }
            }
        }
        .refreshable {
            await model.refresh()
        }
    }

    @ViewBuilder
    private var statusSection: some View {
        Section {
            LabeledContent("Data", value: model.dataAgeText)
            if model.snapshot?.stale == true {
                Label("Snapshot is stale — last successful poll kept", systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                    .font(.subheadline)
            }
            if let err = model.errorMessage {
                Text(err)
                    .font(.footnote)
                    .foregroundStyle(.red)
            }
        }
    }

    private func orderedProviders(_ providers: [Provider]) -> [Provider] {
        let order = model.preferences.providerOrder
        let byID = Dictionary(uniqueKeysWithValues: providers.map { ($0.id, $0) })
        var result: [Provider] = []
        var seen = Set<String>()
        for id in order {
            if let p = byID[id] {
                result.append(p)
                seen.insert(id)
            }
        }
        for p in providers where !seen.contains(p.id) {
            result.append(p)
        }
        return result
    }
}

struct ProviderSection: View {
    let provider: Provider

    var body: some View {
        if let error = provider.error, !error.isEmpty {
            Label(error, systemImage: "xmark.octagon")
                .foregroundStyle(.red)
        }
        if let credits = provider.credits {
            LabeledContent("Reset credits", value: "\(credits.availableCount)")
        }
        if provider.windows.isEmpty && (provider.error == nil || provider.error?.isEmpty == true) {
            Text("No rate windows")
                .foregroundStyle(.secondary)
        }
        ForEach(provider.windows) { window in
            WindowRow(window: window)
        }
    }
}

struct WindowRow: View {
    let window: UsageWindow

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(window.title)
                    .font(.subheadline.weight(.semibold))
                Spacer()
                Text(String(format: "%.0f%% left", window.remainingPercent))
                    .font(.subheadline.monospacedDigit())
                    .foregroundStyle(window.remainingPercent <= 10 ? .red : .secondary)
            }
            ProgressView(value: min(max(window.usedPercent / 100, 0), 1))
                .tint(progressTint(used: window.usedPercent, remaining: window.remainingPercent))
            HStack {
                Text(String(format: "%.1f%% used", window.usedPercent))
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                Spacer()
                if let resets = window.resetsAt {
                    Text("Resets \(RelativeTime.string(for: resets))")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(.vertical, 2)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(accessibilityLabel)
    }

    private var accessibilityLabel: String {
        var parts = ["\(window.title)", String(format: "%.0f percent used", window.usedPercent), String(format: "%.0f percent remaining", window.remainingPercent)]
        if let resets = window.resetsAt {
            parts.append("resets \(RelativeTime.string(for: resets))")
        }
        return parts.joined(separator: ", ")
    }

    private func progressTint(used: Double, remaining: Double) -> Color {
        if remaining <= 10 { return .red }
        if used >= 10 { return .orange }
        return .accentColor
    }
}
