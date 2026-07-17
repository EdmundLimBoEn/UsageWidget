import SwiftUI
import UserNotifications
import UIKit

struct SettingsView: View {
    @Environment(AppModel.self) private var model
    @State private var showSetup = false

    var body: some View {
        @Bindable var model = model
        Form {
            Section("Connection") {
                LabeledContent("Server", value: model.credentials?.serverURL ?? "—")
                    .lineLimit(2)
                Button("Edit connection…") { showSetup = true }
                Button {
                    Task { await model.refresh() }
                } label: {
                    Label("Refresh now", systemImage: "arrow.clockwise")
                }
            }

            Section("Polling") {
                Picker("Interval", selection: $model.settings.pollIntervalMinutes) {
                    ForEach(AppConstants.validPollIntervals, id: \.self) { m in
                        Text(m == 1 ? "1 minute" : "\(m) minutes").tag(m)
                    }
                }
                .onChange(of: model.settings.pollIntervalMinutes) { _, _ in
                    Task { await model.applySettings() }
                }
            }

            Section {
                Toggle("Notifications", isOn: $model.settings.notificationsEnabled)
                    .onChange(of: model.settings.notificationsEnabled) { _, _ in
                        Task { await model.applySettings() }
                    }
                Stepper(
                    "Early alert at \(Int(model.settings.earlyThresholdPct))% used",
                    value: $model.settings.earlyThresholdPct,
                    in: 1...99,
                    step: 1
                )
                .onChange(of: model.settings.earlyThresholdPct) { _, _ in
                    Task { await model.applySettings() }
                }
                Stepper(
                    "Danger alert at \(Int(model.settings.dangerThresholdPct))% remaining",
                    value: $model.settings.dangerThresholdPct,
                    in: 1...99,
                    step: 1
                )
                .onChange(of: model.settings.dangerThresholdPct) { _, _ in
                    Task { await model.applySettings() }
                }
                Button("Request notification permission") {
                    Task { await requestNotifications() }
                }
                LabeledContent("Permission", value: model.notificationStatus)
            } header: {
                Text("Alerts")
            } footer: {
                Text("Alerts apply to every visible provider and every rate window. Hiding a provider disables its widget row and alerts.")
            }

            Section("Providers") {
                if let providers = model.snapshot?.providers {
                    ForEach(providerRows(providers), id: \.id) { row in
                        HStack {
                            VStack(alignment: .leading) {
                                Text(row.name)
                                Text(row.id)
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                            Toggle(
                                "Visible",
                                isOn: Binding(
                                    get: { !model.preferences.hiddenSet.contains(row.id) },
                                    set: { model.setHidden(row.id, hidden: !$0) }
                                )
                            )
                            .labelsHidden()
                        }
                    }
                    .onMove { source, dest in
                        model.moveProvider(from: source, to: dest)
                    }
                } else {
                    Text("No providers yet")
                        .foregroundStyle(.secondary)
                }
            }

            Section("Server health") {
                if let h = model.health {
                    LabeledContent("Service", value: h.service)
                    LabeledContent("CodexBar", value: h.codexbar ? "ok" : "down")
                    LabeledContent("Database", value: h.database ? "ok" : "error")
                    LabeledContent("Polling", value: h.polling ? "running" : "stopped")
                    LabeledContent("APNs", value: h.apns ? "configured" : "noop")
                    if let last = h.lastSuccessAt {
                        LabeledContent("Last success", value: last.formatted())
                    }
                    if let last = h.lastPollAt {
                        LabeledContent("Last poll", value: last.formatted())
                    }
                } else {
                    Text("Unknown — refresh to load health")
                        .foregroundStyle(.secondary)
                }
                LabeledContent("Local data", value: model.dataAgeText)
                if model.snapshot?.stale == true {
                    Label("Stale snapshot", systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.orange)
                }
            }

            if let err = model.errorMessage {
                Section {
                    Text(err).foregroundStyle(.red).font(.footnote)
                }
            }
        }
        .navigationTitle("Settings")
        .toolbar {
            EditButton()
        }
        .sheet(isPresented: $showSetup) {
            NavigationStack {
                SetupView()
                    .toolbar {
                        ToolbarItem(placement: .cancellationAction) {
                            Button("Done") { showSetup = false }
                        }
                    }
            }
        }
        .task {
            await refreshNotificationStatus()
        }
    }

    private struct Row: Identifiable {
        let id: String
        let name: String
    }

    private func providerRows(_ providers: [Provider]) -> [Row] {
        let ordered = ProviderDisplay.orderedVisible(
            providers: providers,
            order: model.preferences.providerOrder,
            hidden: []
        )
        var rows = ordered.map { Row(id: $0.id, name: $0.name) }
        let seen = Set(rows.map(\.id))
        for p in providers where !seen.contains(p.id) {
            rows.append(Row(id: p.id, name: p.name))
        }
        // Include hidden-only order entries without live data
        for id in model.preferences.providerOrder where !rows.contains(where: { $0.id == id }) {
            rows.append(Row(id: id, name: id))
        }
        return rows
    }

    private func requestNotifications() async {
        do {
            let granted = try await UNUserNotificationCenter.current()
                .requestAuthorization(options: [.alert, .sound, .badge])
            await MainActor.run {
                model.notificationStatus = granted ? "authorized" : "denied"
            }
            if granted {
                await MainActor.run {
                    UIApplication.shared.registerForRemoteNotifications()
                }
            }
        } catch {
            await MainActor.run {
                model.notificationStatus = "error"
                model.errorMessage = error.localizedDescription
            }
        }
        await refreshNotificationStatus()
    }

    private func refreshNotificationStatus() async {
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        let text: String
        switch settings.authorizationStatus {
        case .notDetermined: text = "not determined"
        case .denied: text = "denied"
        case .authorized: text = "authorized"
        case .provisional: text = "provisional"
        case .ephemeral: text = "ephemeral"
        @unknown default: text = "unknown"
        }
        await MainActor.run {
            model.notificationStatus = text
        }
    }
}
