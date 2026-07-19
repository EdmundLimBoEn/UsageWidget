import SwiftUI

struct AlertRulesView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State private var draft = ServerSettings()
    @State private var isSaving = false
    @State private var saveError: String?

    var body: some View {
        Form {
            Section("Global defaults") {
                AlertRuleEditor(rule: globalBinding)
            }
            Section {
                Toggle("Quiet hours", isOn: $draft.quietHours.enabled)
                DatePicker("Start", selection: minuteBinding(\.startMinute), displayedComponents: .hourAndMinute)
                DatePicker("End", selection: minuteBinding(\.endMinute), displayedComponents: .hourAndMinute)
                LabeledContent("Time zone", value: draft.quietHours.timeZone)
            } header: { Text("Quiet hours") } footer: {
                Text("During quiet hours, automatic alerts are delivered passively without sound. Device readiness tests remain audible.")
            }

            ForEach(providerIDs, id: \.self) { providerID in
                Section(providerName(providerID)) {
                    Toggle("Use global defaults", isOn: inheritanceBinding(providerID: providerID, windowID: nil))
                    if providerOverride(providerID) != nil {
                        AlertRuleEditor(rule: overrideBinding(providerID: providerID, windowID: nil))
                    }
                    ForEach(windows(providerID)) { window in
                        DisclosureGroup(window.title) {
                            Toggle("Use provider settings", isOn: inheritanceBinding(providerID: providerID, windowID: window.id))
                            if windowOverride(providerID, window.id) != nil {
                                AlertRuleEditor(rule: overrideBinding(providerID: providerID, windowID: window.id))
                            }
                        }
                    }
                }
            }
            if let saveError { Section { Text(saveError).foregroundStyle(.red).font(.footnote) } }
        }
        .navigationTitle("Alert Rules")
        .navigationBarBackButtonHidden(isSaving)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Save") { Task { await save() } }.disabled(isSaving)
            }
        }
        .onAppear {
            draft = model.settings
            let current = TimeZone.current.identifier
            draft.quietHours.timeZone = TimeZone.knownTimeZoneIdentifiers.contains(current) ? current : "UTC"
        }
    }

    private var globalBinding: Binding<AlertRule> {
        Binding(get: { draft.globalAlertRule }, set: { draft.globalAlertRule = $0 })
    }

    private var providerIDs: [String] {
        var ids = model.preferences.providerOrder + draft.alertOverrides.map(\.providerID)
        ids += model.snapshot?.providers.map(\.id) ?? []
        var seen = Set<String>(); return ids.filter { seen.insert($0).inserted }
    }

    private func providerName(_ id: String) -> String { model.snapshot?.providers.first(where: { $0.id == id })?.name ?? id }
    private func windows(_ providerID: String) -> [UsageWindow] {
        var values = model.snapshot?.providers.first(where: { $0.id == providerID })?.windows ?? []
        for override in draft.alertOverrides where override.providerID == providerID && override.windowID != nil {
            if !values.contains(where: { $0.id == override.windowID! }) {
                values.append(UsageWindow(id: override.windowID!, key: override.windowID!, title: override.windowID!, usedPercent: 0, remainingPercent: 100))
            }
        }
        return values
    }
    private func providerOverride(_ id: String) -> AlertOverride? { draft.alertOverrides.first { $0.providerID == id && $0.windowID == nil } }
    private func windowOverride(_ providerID: String, _ windowID: String) -> AlertOverride? { draft.alertOverrides.first { $0.providerID == providerID && $0.windowID == windowID } }

    private func inheritanceBinding(providerID: String, windowID: String?) -> Binding<Bool> {
        Binding(get: { !draft.alertOverrides.contains { $0.providerID == providerID && $0.windowID == windowID } }, set: { inherited in
            draft.alertOverrides.removeAll { $0.providerID == providerID && $0.windowID == windowID }
            if !inherited {
                let effective = draft.effectiveRule(providerID: providerID, windowID: windowID)
                draft.alertOverrides.append(AlertOverride(providerID: providerID, windowID: windowID, rule: effective))
            }
        })
    }

    private func overrideBinding(providerID: String, windowID: String?) -> Binding<AlertRule> {
        Binding(get: { draft.alertOverrides.first { $0.providerID == providerID && $0.windowID == windowID }?.rule ?? draft.effectiveRule(providerID: providerID, windowID: windowID) }, set: { value in
            if let index = draft.alertOverrides.firstIndex(where: { $0.providerID == providerID && $0.windowID == windowID }) { draft.alertOverrides[index].rule = value }
        })
    }

    private func minuteBinding(_ keyPath: WritableKeyPath<QuietHours, Int>) -> Binding<Date> {
        Binding(get: {
            let minute = draft.quietHours[keyPath: keyPath]
            return Calendar.current.date(bySettingHour: minute / 60, minute: minute % 60, second: 0, of: Date()) ?? Date()
        }, set: { date in
            let parts = Calendar.current.dateComponents([.hour, .minute], from: date)
            draft.quietHours[keyPath: keyPath] = (parts.hour ?? 0) * 60 + (parts.minute ?? 0)
            draft.quietHours.timeZone = TimeZone.current.identifier
        })
    }

    private func save() async {
        isSaving = true; defer { isSaving = false }
        do { try await model.saveSettings(draft); dismiss() } catch { saveError = String(describing: error) }
    }
}

private struct AlertRuleEditor: View {
    @Binding var rule: AlertRule
    var body: some View {
        Toggle("Enabled", isOn: $rule.enabled)
        Stepper("Early at \(Int(rule.earlyThresholdPct))% used", value: $rule.earlyThresholdPct, in: 1...99)
        Stepper("Danger at \(Int(rule.dangerThresholdPct))% left", value: $rule.dangerThresholdPct, in: 1...99)
        Picker("Danger reminders", selection: $rule.repeatIntervalMinutes) {
            Text("Never").tag(0); Text("Every hour").tag(60); Text("Every 3 hours").tag(180); Text("Every 6 hours").tag(360)
        }
    }
}
