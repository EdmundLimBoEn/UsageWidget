import SwiftUI
import UserNotifications
import UIKit

struct ReadinessView: View {
    @Environment(AppModel.self) private var model

    private var state: DeliveryState { model.deliveryState }

    var body: some View {
        List {
            Section {
                Label(state.title, systemImage: icon)
                    .font(.title2.weight(.semibold)).foregroundStyle(tint)
                Text(state.explanation)
                    .font(.footnote).foregroundStyle(.secondary)
            }
            if model.offersNotificationPermission {
                Section("This iPhone") {
                    readinessRow(title: "Notification permission", status: model.notificationsAuthorized ? "pass" : "fail", detail: model.notificationStatusLabel)
                    Button("Request notification permission") { Task { await requestPermission() } }
                }
            }
            Section("Server") {
                if let checks = model.readiness?.checks {
                    ForEach(state.relevantChecks(checks)) { check in readinessRow(title: check.title, status: check.status, detail: check.detail) }
                } else { ProgressView("Loading checks…") }
            }
            Section {
                Button("Refresh checks") { Task { await model.refreshReadiness() } }
                Button("Collect now") { Task { await model.forcePoll(); await model.refreshReadiness() } }
                if model.offersNotificationPermission {
                    Button("Send a test alert") { Task { await model.runReadinessTest() } }
                        .disabled(model.isTestingAction)
                }
                if model.isTestingAction {
                    ProgressView()
                }
                if let status = model.statusMessage {
                    Text(status).font(.footnote).foregroundStyle(.secondary)
                }
                if let note = model.readiness?.latestTest?.acceptanceNote { Text(note).font(.footnote).foregroundStyle(.secondary) }
            }
        }
        .navigationTitle("Delivery")
        .task { await model.registerTokensIfNeeded(); await model.refreshReadiness() }
        .refreshable { await model.refreshReadiness() }
    }

    private var icon: String {
        switch state {
        case .checking: "arrow.trianglehead.2.clockwise.rotate.90"
        case .dashboardOnly: "rectangle.grid.1x2"
        case .ready: "checkmark.circle.fill"
        case .needsAttention: "exclamationmark.triangle.fill"
        }
    }

    private var tint: Color {
        switch state {
        case .checking, .dashboardOnly: .secondary
        case .ready: .green
        case .needsAttention: .orange
        }
    }

    private func readinessRow(title: String, status: String, detail: String) -> some View {
        HStack(alignment: .top) {
            Image(systemName: status == "pass" ? "checkmark.circle.fill" : status == "warning" ? "exclamationmark.circle.fill" : "xmark.circle.fill")
                .foregroundStyle(status == "pass" ? .green : status == "warning" ? .orange : .red)
            VStack(alignment: .leading) { Text(title); Text(detail).font(.caption).foregroundStyle(.secondary) }
        }
    }

    private func requestPermission() async {
        guard model.offersNotificationPermission else { return }
        _ = try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge])
        await MainActor.run { UIApplication.shared.registerForRemoteNotifications() }
        await model.refreshReadiness()
    }
}
