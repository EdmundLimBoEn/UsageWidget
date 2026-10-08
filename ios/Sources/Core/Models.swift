import Foundation

public struct Snapshot: Codable, Equatable, Sendable {
    public var fetchedAt: Date
    public var stale: Bool
    public var providers: [Provider]
    public var pollIntervalMinutes: Int
    public var sourceKind: String?

    public init(fetchedAt: Date, stale: Bool, providers: [Provider], pollIntervalMinutes: Int, sourceKind: String? = nil) {
        self.fetchedAt = fetchedAt
        self.stale = stale
        self.providers = providers
        self.pollIntervalMinutes = pollIntervalMinutes
        self.sourceKind = sourceKind
    }
}

public struct Provider: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var error: String?
    public var stale: Bool
    public var windows: [UsageWindow]
    public var credits: Credits?

    public init(
        id: String,
        name: String,
        error: String? = nil,
        stale: Bool = false,
        windows: [UsageWindow] = [],
        credits: Credits? = nil
    ) {
        self.id = id
        self.name = name
        self.error = error
        self.stale = stale
        self.windows = windows
        self.credits = credits
    }

    enum CodingKeys: String, CodingKey {
        case id, name, error, stale, windows, credits
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        stale = try c.decodeIfPresent(Bool.self, forKey: .stale) ?? false
        windows = try c.decodeIfPresent([UsageWindow].self, forKey: .windows) ?? []
        credits = try c.decodeIfPresent(Credits.self, forKey: .credits)
    }
}

public struct UsageWindow: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var key: String
    public var title: String
    public var usedPercent: Double
    public var remainingPercent: Double
    public var resetsAt: Date?
    public var windowLabel: String?
    public var forecast: WindowForecast?

    public init(
        id: String,
        key: String,
        title: String,
        usedPercent: Double,
        remainingPercent: Double,
        resetsAt: Date? = nil,
        windowLabel: String? = nil,
        forecast: WindowForecast? = nil
    ) {
        self.id = id
        self.key = key
        self.title = title
        self.usedPercent = usedPercent
        self.remainingPercent = remainingPercent
        self.resetsAt = resetsAt
        self.windowLabel = windowLabel
        self.forecast = forecast
    }
}

public struct WindowForecast: Codable, Equatable, Sendable {
    public var computedAt: Date
    public var burnRatePercentPerHour: Double
    public var estimatedExhaustionAt: Date
    public var exhaustsBeforeReset: Bool
    public var sampleCount: Int
    public var basedOnHours: Double
    public var source: String?
    public var windowLabel: String?
    public var projectedPercentAtReset: Double?
    public var annotation: String?
}

public struct Credits: Codable, Equatable, Sendable {
    public var availableCount: Int

    public init(availableCount: Int) {
        self.availableCount = availableCount
    }
}

public struct Health: Codable, Equatable, Sendable {
    public var service: String
    public var codexbar: Bool
    public var upstream: Bool?
    public var database: Bool
    public var polling: Bool
    public var apns: Bool
    public var lastPollAt: Date?
    public var lastSuccessAt: Date?
    public var collector: CollectorHealth?
    public var widgetDelivery: WidgetDeliveryHealth?
    public var version: String?
    public var schemaVersion: Int?

    public init(
        service: String,
        codexbar: Bool,
        upstream: Bool? = nil,
        database: Bool,
        polling: Bool,
        apns: Bool,
        lastPollAt: Date? = nil,
        lastSuccessAt: Date? = nil,
        collector: CollectorHealth? = nil,
        widgetDelivery: WidgetDeliveryHealth? = nil,
        version: String? = nil,
        schemaVersion: Int? = nil
    ) {
        self.service = service
        self.codexbar = codexbar
        self.upstream = upstream
        self.database = database
        self.polling = polling
        self.apns = apns
        self.lastPollAt = lastPollAt
        self.lastSuccessAt = lastSuccessAt
        self.collector = collector
        self.widgetDelivery = widgetDelivery
        self.version = version
        self.schemaVersion = schemaVersion
    }

    public var upstreamOK: Bool { upstream ?? codexbar }
}

public struct CollectorHealth: Codable, Equatable, Sendable {
    public var source: String
    public var status: String
    public var lastAttemptAt: Date?
    public var lastSuccessAt: Date?
    public var lastChangedAt: Date?
    public var nextAttemptAt: Date?
    public var durationMs: Int
    public var consecutiveFailures: Int
    public var lastError: String?
}

public struct WidgetDeliveryHealth: Codable, Equatable, Sendable {
    public var status: String
    public var lastAttemptAt: Date?
    public var attempted: Int
    public var succeeded: Int
    public var failed: Int
    public var lastError: String?
}

/// How this iPhone gets updates from the connected server.
///
/// A self-hosted server without APNs credentials reports `apns == false` in
/// `/v1/health`. That is a supported setup, not a fault: the dashboard and
/// widget still work, there are just no push alerts. Only a server with APNs
/// configured should ever lead to a notification-permission prompt.
public enum DeliveryState: Equatable, Sendable {
    /// Health/readiness not loaded yet.
    case checking
    /// Server has no APNs credentials: no alerts, widget refreshes on iOS's schedule.
    case dashboardOnly
    /// APNs configured, readiness checks pass, and notifications are allowed.
    case ready
    /// APNs configured but something (permission, collector, device test) fails.
    case needsAttention

    public static func evaluate(health: Health?, readiness: Readiness?, notificationsAuthorized: Bool) -> DeliveryState {
        guard let health else { return .checking }
        guard health.apns else { return .dashboardOnly }
        guard let readiness else { return .checking }
        return readiness.ready && notificationsAuthorized ? .ready : .needsAttention
    }

    /// Server readiness checks (see server/api.go) that only matter when pushes are sent.
    public static let pushOnlyCheckIDs: Set<String> = ["apns", "alert_token", "widget_token", "delivery_test"]

    /// In dashboard-only mode the push-only checks always "fail" by design; hide them so
    /// the list only shows things the user can actually fix (collector, snapshot, ...).
    public func relevantChecks(_ checks: [ReadinessCheck]) -> [ReadinessCheck] {
        guard self == .dashboardOnly else { return checks }
        return checks.filter { !Self.pushOnlyCheckIDs.contains($0.id) }
    }

    /// Never ask for notification permission when the server has told us it cannot
    /// send pushes. While health is unknown (not loaded yet, offline) keep offering it.
    public static func offersNotificationPermission(health: Health?) -> Bool {
        health?.apns != false
    }

    public var title: String {
        switch self {
        case .checking: "Checking delivery"
        case .dashboardOnly: "Dashboard-only"
        case .ready: "Ready"
        case .needsAttention: "Needs attention"
        }
    }

    public var explanation: String {
        switch self {
        case .checking:
            "Loading delivery checks from your server."
        case .dashboardOnly:
            "Your server isn't set up to send push notifications, so there are no alerts. The dashboard updates when you open the app. The widget refreshes on its own schedule, which iOS controls, so it can lag behind the dashboard. Alerts need a self-hosted server with APNs configured."
        case .ready, .needsAttention:
            "Alerts and the widget need notification permission, a working collector, and a recent APNs test."
        }
    }
}

public enum DataFreshness: Equatable, Sendable {
    case collecting
    case current
    case stale
    case unavailable
    case sample
}

public struct ServerSettings: Codable, Equatable, Sendable {
    public var pollIntervalMinutes: Int
    public var providerOrder: [String]
    public var hiddenProviders: [String]
    public var notificationsEnabled: Bool
    public var earlyThresholdPct: Double
    public var dangerThresholdPct: Double
    public var defaultRepeatIntervalMinutes: Int
    public var quietHours: QuietHours
    public var alertOverrides: [AlertOverride]

    public init(
        pollIntervalMinutes: Int = 5,
        providerOrder: [String] = ProviderCatalog.defaultOrder,
        hiddenProviders: [String] = [],
        notificationsEnabled: Bool = true,
        earlyThresholdPct: Double = 10,
        dangerThresholdPct: Double = 10,
        defaultRepeatIntervalMinutes: Int = 0,
        quietHours: QuietHours = QuietHours(),
        alertOverrides: [AlertOverride] = []
    ) {
        self.pollIntervalMinutes = pollIntervalMinutes
        self.providerOrder = providerOrder
        self.hiddenProviders = hiddenProviders
        self.notificationsEnabled = notificationsEnabled
        self.earlyThresholdPct = earlyThresholdPct
        self.dangerThresholdPct = dangerThresholdPct
        self.defaultRepeatIntervalMinutes = defaultRepeatIntervalMinutes
        self.quietHours = quietHours
        self.alertOverrides = alertOverrides
    }

    enum CodingKeys: String, CodingKey {
        case pollIntervalMinutes, providerOrder, hiddenProviders
        case notificationsEnabled, earlyThresholdPct, dangerThresholdPct
        case defaultRepeatIntervalMinutes, quietHours, alertOverrides
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        pollIntervalMinutes = try c.decodeIfPresent(Int.self, forKey: .pollIntervalMinutes) ?? 5
        providerOrder = try c.decodeIfPresent([String].self, forKey: .providerOrder) ?? ProviderCatalog.defaultOrder
        hiddenProviders = try c.decodeIfPresent([String].self, forKey: .hiddenProviders) ?? []
        notificationsEnabled = try c.decodeIfPresent(Bool.self, forKey: .notificationsEnabled) ?? true
        earlyThresholdPct = try c.decodeIfPresent(Double.self, forKey: .earlyThresholdPct) ?? 10
        dangerThresholdPct = try c.decodeIfPresent(Double.self, forKey: .dangerThresholdPct) ?? 10
        defaultRepeatIntervalMinutes = try c.decodeIfPresent(Int.self, forKey: .defaultRepeatIntervalMinutes) ?? 0
        quietHours = try c.decodeIfPresent(QuietHours.self, forKey: .quietHours) ?? QuietHours()
        alertOverrides = try c.decodeIfPresent([AlertOverride].self, forKey: .alertOverrides) ?? []
    }
}

public struct AlertRule: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var earlyThresholdPct: Double
    public var dangerThresholdPct: Double
    public var repeatIntervalMinutes: Int

    public init(enabled: Bool = true, earlyThresholdPct: Double = 10, dangerThresholdPct: Double = 10, repeatIntervalMinutes: Int = 0) {
        self.enabled = enabled; self.earlyThresholdPct = earlyThresholdPct
        self.dangerThresholdPct = dangerThresholdPct; self.repeatIntervalMinutes = repeatIntervalMinutes
    }
}

public struct QuietHours: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var startMinute: Int
    public var endMinute: Int
    public var timeZone: String

    public init(enabled: Bool = false, startMinute: Int = 1320, endMinute: Int = 420, timeZone: String = "UTC") {
        self.enabled = enabled; self.startMinute = startMinute; self.endMinute = endMinute; self.timeZone = timeZone
    }
}

public struct AlertOverride: Codable, Equatable, Sendable, Identifiable {
    public var providerID: String
    public var windowID: String?
    public var rule: AlertRule
    public var id: String { providerID + "\u{0}" + (windowID ?? "") }
}

public extension ServerSettings {
    var globalAlertRule: AlertRule {
        get { AlertRule(enabled: notificationsEnabled, earlyThresholdPct: earlyThresholdPct, dangerThresholdPct: dangerThresholdPct, repeatIntervalMinutes: defaultRepeatIntervalMinutes) }
        set {
            notificationsEnabled = newValue.enabled; earlyThresholdPct = newValue.earlyThresholdPct
            dangerThresholdPct = newValue.dangerThresholdPct; defaultRepeatIntervalMinutes = newValue.repeatIntervalMinutes
        }
    }

    func effectiveRule(providerID: String, windowID: String? = nil) -> AlertRule {
        var result = globalAlertRule
        if let provider = alertOverrides.first(where: { $0.providerID == providerID && $0.windowID == nil }) { result = provider.rule }
        if let windowID, let window = alertOverrides.first(where: { $0.providerID == providerID && $0.windowID == windowID }) { result = window.rule }
        return result
    }
}

public struct ReadinessCheck: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var title: String
    public var status: String
    public var detail: String
    public var core: Bool
}

public struct ReadinessTestResult: Codable, Equatable, Sendable {
    public var attemptedAt: Date
    public var alertAttempted: Bool
    public var alertAccepted: Bool
    public var widgetAttempted: Bool
    public var widgetAccepted: Bool
    public var acceptanceNote: String
}

public struct Readiness: Codable, Equatable, Sendable {
    public var ready: Bool
    public var checkedAt: Date
    public var checks: [ReadinessCheck]
    public var latestTest: ReadinessTestResult?
}

public struct QRConfiguration: Equatable, Sendable {
    public var serverURL: String
    public var token: String

    public static func parse(_ payload: String) throws -> QRConfiguration {
        guard let components = URLComponents(string: payload),
              components.scheme == "usagewidget", components.host == "configure",
              components.path.isEmpty else { throw QRConfigurationError.invalidAction }
        let items = components.queryItems ?? []
        guard items.count == 3, Set(items.map(\.name)) == Set(["v", "server", "token"]),
              items.filter({ $0.name == "v" }).count == 1,
              items.first(where: { $0.name == "v" })?.value == "1" else { throw QRConfigurationError.invalidVersion }
        guard let server = items.first(where: { $0.name == "server" })?.value,
              let url = URL(string: server), url.scheme == "https", url.host != nil else { throw QRConfigurationError.insecureServer }
        guard let token = items.first(where: { $0.name == "token" })?.value,
              token.count >= 32, token == token.trimmingCharacters(in: .whitespacesAndNewlines) else { throw QRConfigurationError.invalidToken }
        return QRConfiguration(serverURL: server, token: token)
    }
}

public enum QRConfigurationError: Error, Equatable {
    case invalidAction, invalidVersion, insecureServer, invalidToken
}

public struct DeviceRegistration: Codable, Equatable, Sendable {
    public var deviceID: String
    public var apnsToken: String?
    public var widgetToken: String?

    public init(deviceID: String, apnsToken: String? = nil, widgetToken: String? = nil) {
        self.deviceID = deviceID
        self.apnsToken = apnsToken
        self.widgetToken = widgetToken
    }
}

public struct PollResult: Codable, Equatable, Sendable {
    public var ok: Bool
    public var polledAt: Date
    public var success: Bool
    public var events: Int
    public var snapshotChanged: Bool
    public var error: String?

    public init(
        ok: Bool,
        polledAt: Date,
        success: Bool,
        events: Int,
        snapshotChanged: Bool,
        error: String? = nil
    ) {
        self.ok = ok
        self.polledAt = polledAt
        self.success = success
        self.events = events
        self.snapshotChanged = snapshotChanged
        self.error = error
    }
}

public enum AppConstants {
    public static let appGroupID = "group.systems.edmundlim.usagewidget"
    public static let keychainService = "systems.edmundlim.UsageWidget"
    public static let privacyPolicyURL = URL(string: "https://github.com/EdmundLimBoEn/UsageWidget/blob/master/PRIVACY.md")!
    public static let supportURL = URL(string: "https://github.com/EdmundLimBoEn/UsageWidget/issues")!
    public static let affiliationDisclaimer = "UsageWidget is not affiliated with, endorsed by, or sponsored by the providers whose usage it can display."
    public static var keychainAccessGroup: String? {
        guard let value = Bundle.main.object(forInfoDictionaryKey: "UsageWidgetKeychainAccessGroup") as? String,
              !value.isEmpty, !value.contains("$(") else { return nil }
        return value
    }
    public static let validPollIntervals = [1, 5, 15, 30, 60]
}

public enum JSONCoding {
    public static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let c = try decoder.singleValueContainer()
            let s = try c.decode(String.self)
            let basic = Date.ISO8601FormatStyle(includingFractionalSeconds: false)
            let fractional = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
            if let date = try? Date(s, strategy: basic) {
                return date
            }
            if let date = try? Date(s, strategy: fractional) {
                return date
            }
            throw DecodingError.dataCorruptedError(in: c, debugDescription: "Invalid date: \(s)")
        }
        return d
    }()

    public static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .custom { date, encoder in
            var c = encoder.singleValueContainer()
            try c.encode(date.formatted(Date.ISO8601FormatStyle(includingFractionalSeconds: false)))
        }
        return e
    }()
}

public enum RelativeTime {
    public static func string(for date: Date, relativeTo now: Date = Date()) -> String {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .abbreviated
        return f.localizedString(for: date, relativeTo: now)
    }
}

public enum ForecastText {
    public static func string(for window: UsageWindow, now: Date = Date()) -> String? {
        guard let forecast = window.forecast else { return nil }
        if let annotation = forecast.annotation, !annotation.isEmpty {
            if let resetPart = annotation.split(separator: "·").map({ $0.trimmingCharacters(in: .whitespaces) }).last,
               annotation.contains("·") {
                return String(resetPart)
            }
            if annotation.hasPrefix("resets ") {
                return nil
            }
            return annotation
        }
        if let projected = forecast.projectedPercentAtReset, !forecast.exhaustsBeforeReset {
            return String(format: "~%.0f%% by reset", projected)
        }
        if forecast.exhaustsBeforeReset {
            return "Likely out \(RelativeTime.string(for: forecast.estimatedExhaustionAt, relativeTo: now))"
        }
        return "On track until reset"
    }
}

public enum ProviderCatalog {
    public static let defaultOrder = ["cursor", "codex", "claude_code", "copilot", "gemini_cli", "grok", "devin"]
}

public enum ProviderDisplay {
    /// Visible providers in user order, then any remaining visible ones.
    public static func orderedVisible(
        providers: [Provider],
        order: [String],
        hidden: Set<String>
    ) -> [Provider] {
        var byID: [String: Provider] = [:]
        byID.reserveCapacity(providers.count)
        for provider in providers where byID[provider.id] == nil {
            byID[provider.id] = provider
        }
        var seen = Set<String>()
        var result: [Provider] = []
        for id in order {
            guard !hidden.contains(id), let p = byID[id] else { continue }
            result.append(p)
            seen.insert(id)
        }
        for p in providers where !seen.contains(p.id) && !hidden.contains(p.id) {
            result.append(p)
            seen.insert(p.id)
        }
        return result
    }
}
