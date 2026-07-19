import SwiftUI
import UserNotifications
import UIKit

struct ReadinessView: View {
    @Environment(AppModel.self) private var model

    private var locallyAuthorized: Bool { ["authorized", "provisional", "ephemeral"].contains(model.notificationStatus) }
    private var ready: Bool { model.readiness?.ready == true && locallyAuthorized }

    var body: some View {
        List {
            Section {
                Label(ready ? "Ready" : "Needs attention", systemImage: ready ? "checkmark.seal.fill" : "exclamationmark.triangle.fill")
                    .font(.title2.weight(.semibold)).foregroundStyle(ready ? .green : .orange)
                Text("Ready requires every core server check, local notification authorization, and an APNs-accepted device test in the last 15 minutes.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
            Section("This iPhone") {
                readinessRow(title: "Notification permission", status: locallyAuthorized ? "pass" : "fail", detail: model.notificationStatus)
                Button("Request notification permission") { Task { await requestPermission() } }
            }
            Section("Server checks") {
                if let checks = model.readiness?.checks {
                    ForEach(checks) { check in readinessRow(title: check.title, status: check.status, detail: check.detail) }
                } else { ProgressView("Loading checks…") }
            }
            Section {
                Button("Refresh checks") { Task { await model.refreshReadiness() } }
                Button("Poll server now") { Task { await model.forcePoll(); await model.refreshReadiness() } }
                Button("Send device test") { Task { await model.runReadinessTest() } }
                    .disabled(model.isTestingAction)
                if let note = model.readiness?.latestTest?.acceptanceNote { Text(note).font(.footnote).foregroundStyle(.secondary) }
            }
        }
        .navigationTitle("Release readiness")
        .task { await model.registerTokensIfNeeded(); await model.refreshReadiness() }
        .refreshable { await model.refreshReadiness() }
    }

    private func readinessRow(title: String, status: String, detail: String) -> some View {
        HStack(alignment: .top) {
            Image(systemName: status == "pass" ? "checkmark.circle.fill" : status == "warning" ? "exclamationmark.circle.fill" : "xmark.circle.fill")
                .foregroundStyle(status == "pass" ? .green : status == "warning" ? .orange : .red)
            VStack(alignment: .leading) { Text(title); Text(detail).font(.caption).foregroundStyle(.secondary) }
        }
    }

    private func requestPermission() async {
        _ = try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge])
        await MainActor.run { UIApplication.shared.registerForRemoteNotifications() }
        await model.refreshReadiness()
    }
}
