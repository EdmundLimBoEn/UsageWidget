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
                Button(model.isConfigured ? "Edit connection…" : "Connect a server…") { showSetup = true }
            }

            if model.isConfigured {
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
                    if model.deliveryState == .dashboardOnly {
                        LabeledContent("Delivery", value: DeliveryState.dashboardOnly.title)
                    } else {
                        NavigationLink { AlertRulesView() } label: { Label("Alert rules", systemImage: "bell.and.waves.left.and.right") }
                        if model.offersNotificationPermission {
                            Button("Request notification permission") {
                                Task { await requestNotifications() }
                            }
                        }
                        LabeledContent("Permission", value: model.notificationStatusLabel)
                    }
                } header: {
                    Text("Alerts")
                } footer: {
                    if model.deliveryState == .dashboardOnly {
                        Text("Your server isn't set up to send push notifications. The widget refreshes on its own schedule, which iOS controls. Alerts need a self-hosted server with APNs configured.")
                    }
                }
            }

            Section {
                ForEach(model.providerRows) { row in
                    HStack {
                        ProviderMark(size: 28, cornerRadius: 8)
                            .opacity(row.available ? 1 : 0.4)
                        VStack(alignment: .leading, spacing: 3) {
                            Text(row.name)
                                .foregroundStyle(row.available ? .primary : .secondary)
                            Text(row.statusText)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Toggle(
                            "Show \(row.name)",
                            isOn: Binding(
                                get: { row.available && !model.preferences.hiddenSet.contains(row.id) },
                                set: { model.setHidden(row.id, hidden: !$0) }
                            )
                        )
                        .labelsHidden()
                        .disabled(!row.available)
                        .accessibilityLabel("Show \(row.name)")
                        .accessibilityValue(row.available ? (model.preferences.hiddenSet.contains(row.id) ? "Hidden" : "Visible") : "Unavailable")
                        .accessibilityHint(row.statusText)
                    }
                }
                .onMove { source, dest in
                    model.moveProvider(from: source, to: dest)
                }
                if model.isConfigured {
                    Button("Refresh provider availability") {
                        Task { await model.forcePoll() }
                    }
                    .disabled(model.isTestingAction || model.isLoading)
                }
            } header: {
                Text("Providers")
            } footer: {
                Text("All supported providers appear here. Log in on the machine running CrossUsage, then refresh to enable a provider. Hiding a provider removes it from the widget and stops its alerts.")
            }

            if model.isConfigured {
                Section {
                    NavigationLink { ReadinessView() } label: {
                        Label("Delivery", systemImage: "iphone.radiowaves.left.and.right")
                    }
                } header: {
                    Text("This iPhone")
                } footer: {
                    Text("Checks that alerts and the widget can reach this phone.")
                }
            }

            if let err = model.errorMessage {
                Section {
                    Text(err).foregroundStyle(.red).font(.footnote)
                }
            }

            if model.homeSurface == .samplePreview {
                Section {
                    Button("Exit sample preview") {
                        model.exitSamplePreview()
                    }
                } footer: {
                    Text("Return to server setup. Sample data is not saved.")
                }
            }

            Section {
                Link(destination: AppConstants.privacyPolicyURL) {
                    Label("Privacy Policy", systemImage: "hand.raised")
                }
                Link(destination: AppConstants.supportURL) {
                    Label("Support", systemImage: "questionmark.circle")
                }
            } footer: {
                Text(AppConstants.affiliationDisclaimer)
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

    private func requestNotifications() async {
        guard model.offersNotificationPermission else { return }
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
