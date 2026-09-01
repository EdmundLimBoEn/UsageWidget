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
                NavigationLink { AlertRulesView() } label: { Label("Alert rules", systemImage: "bell.and.waves.left.and.right") }
                Button("Request notification permission") {
                    Task { await requestNotifications() }
                }
                LabeledContent("Permission", value: model.notificationStatusLabel)
            } header: {
                Text("Alerts")
            }

            Section {
                if let providers = model.snapshot?.providers {
                    ForEach(providerRows(providers), id: \.id) { row in
                        HStack {
                            ProviderMark(providerID: row.id, providerName: row.name, size: 28, cornerRadius: 8)
                            Text(row.name)
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
            } header: {
                Text("Providers")
            } footer: {
                Text("Hiding a provider removes it from the widget and stops its alerts.")
            }

            Section {
                NavigationLink { ReadinessView() } label: {
                    Label("Delivery", systemImage: "iphone.radiowaves.left.and.right")
                }
            } header: {
                Text("This iPhone")
            } footer: {
                Text("Checks that alerts and the widget can reach this phone.")
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
        ProviderDisplay.orderedVisible(
            providers: providers,
            order: model.preferences.providerOrder,
            hidden: []
        ).map { Row(id: $0.id, name: $0.name) }
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
