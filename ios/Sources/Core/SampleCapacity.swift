import Foundation

public enum SampleCapacity {
    public static var snapshot: Snapshot {
        Snapshot(
            fetchedAt: Date().addingTimeInterval(-120),
            stale: false,
            providers: [
                Provider(id: "cursor", name: "Cursor", windows: [
                    UsageWindow(id: "cursor.plan-percent-used", key: "plan-percent-used", title: "Plan", usedPercent: 45, remainingPercent: 55, windowLabel: "30d"),
                    UsageWindow(id: "cursor.plan-auto-percent-used", key: "plan-auto-percent-used", title: "Auto", usedPercent: 12, remainingPercent: 88, windowLabel: "30d"),
                ]),
                Provider(id: "codex", name: "Codex", windows: [
                    UsageWindow(id: "codex.primary", key: "primary", title: "5h", usedPercent: 42, remainingPercent: 58, windowLabel: "5h"),
                    UsageWindow(id: "codex.secondary", key: "secondary", title: "Weekly", usedPercent: 11, remainingPercent: 89, windowLabel: "7d"),
                ]),
                Provider(id: "claude_code", name: "Claude Code", windows: [
                    UsageWindow(id: "claude_code.usage-five-hour", key: "usage-five-hour", title: "5h", usedPercent: 20, remainingPercent: 80, windowLabel: "5h"),
                ]),
            ],
            pollIntervalMinutes: 5,
            sourceKind: "sample"
        )
    }
}
