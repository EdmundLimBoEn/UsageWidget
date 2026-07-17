import Foundation

public struct Snapshot: Codable, Equatable, Sendable {
    public var fetchedAt: Date
    public var stale: Bool
    public var providers: [Provider]
    public var pollIntervalMinutes: Int

    public init(fetchedAt: Date, stale: Bool, providers: [Provider], pollIntervalMinutes: Int) {
        self.fetchedAt = fetchedAt
        self.stale = stale
        self.providers = providers
        self.pollIntervalMinutes = pollIntervalMinutes
    }
}

public struct Provider: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var error: String?
    public var windows: [UsageWindow]
    public var credits: Credits?
    public var raw: Data?

    public init(
        id: String,
        name: String,
        error: String? = nil,
        windows: [UsageWindow] = [],
        credits: Credits? = nil,
        raw: Data? = nil
    ) {
        self.id = id
        self.name = name
        self.error = error
        self.windows = windows
        self.credits = credits
        self.raw = raw
    }

    enum CodingKeys: String, CodingKey {
        case id, name, error, windows, credits, raw
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        windows = try c.decodeIfPresent([UsageWindow].self, forKey: .windows) ?? []
        credits = try c.decodeIfPresent(Credits.self, forKey: .credits)
        if let rawMessage = try? c.decodeIfPresent(Data.self, forKey: .raw) {
            raw = rawMessage
        } else if let rawObject = try? c.decodeIfPresent([String: JSONValue].self, forKey: .raw) {
            raw = try? JSONEncoder().encode(rawObject)
        } else {
            raw = nil
        }
    }
}

public struct UsageWindow: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var key: String
    public var title: String
    public var usedPercent: Double
    public var remainingPercent: Double
    public var resetsAt: Date?

    public init(
        id: String,
        key: String,
        title: String,
        usedPercent: Double,
        remainingPercent: Double,
        resetsAt: Date? = nil
    ) {
        self.id = id
        self.key = key
        self.title = title
        self.usedPercent = usedPercent
        self.remainingPercent = remainingPercent
        self.resetsAt = resetsAt
    }
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
    public var database: Bool
    public var polling: Bool
    public var apns: Bool
    public var lastPollAt: Date?
    public var lastSuccessAt: Date?

    public init(
        service: String,
        codexbar: Bool,
        database: Bool,
        polling: Bool,
        apns: Bool,
        lastPollAt: Date? = nil,
        lastSuccessAt: Date? = nil
    ) {
        self.service = service
        self.codexbar = codexbar
        self.database = database
        self.polling = polling
        self.apns = apns
        self.lastPollAt = lastPollAt
        self.lastSuccessAt = lastSuccessAt
    }
}

public struct ServerSettings: Codable, Equatable, Sendable {
    public var pollIntervalMinutes: Int
    public var providerOrder: [String]
    public var hiddenProviders: [String]
    public var notificationsEnabled: Bool
    public var earlyThresholdPct: Double
    public var dangerThresholdPct: Double

    public init(
        pollIntervalMinutes: Int = 5,
        providerOrder: [String] = ["codex", "claude", "grok"],
        hiddenProviders: [String] = [],
        notificationsEnabled: Bool = true,
        earlyThresholdPct: Double = 10,
        dangerThresholdPct: Double = 10
    ) {
        self.pollIntervalMinutes = pollIntervalMinutes
        self.providerOrder = providerOrder
        self.hiddenProviders = hiddenProviders
        self.notificationsEnabled = notificationsEnabled
        self.earlyThresholdPct = earlyThresholdPct
        self.dangerThresholdPct = dangerThresholdPct
    }
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

public enum AppConstants {
    public static let appGroupID = "group.systems.edmundlim.usagewidget"
    public static let keychainService = "systems.edmundlim.UsageWidget"
    public static let keychainAccessGroup = "systems.edmundlim.UsageWidget"
    public static let validPollIntervals = [1, 5, 15, 30, 60]
}

// Lightweight JSON passthrough for unknown nested objects.
public enum JSONValue: Codable, Equatable, Sendable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case object([String: JSONValue])
    case array([JSONValue])
    case null

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() {
            self = .null
        } else if let v = try? c.decode(Bool.self) {
            self = .bool(v)
        } else if let v = try? c.decode(Double.self) {
            self = .number(v)
        } else if let v = try? c.decode(String.self) {
            self = .string(v)
        } else if let v = try? c.decode([String: JSONValue].self) {
            self = .object(v)
        } else if let v = try? c.decode([JSONValue].self) {
            self = .array(v)
        } else {
            self = .null
        }
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .string(let v): try c.encode(v)
        case .number(let v): try c.encode(v)
        case .bool(let v): try c.encode(v)
        case .object(let v): try c.encode(v)
        case .array(let v): try c.encode(v)
        case .null: try c.encodeNil()
        }
    }
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

public enum ProviderDisplay {
    /// Visible providers in user order, then any remaining visible ones.
    public static func orderedVisible(
        providers: [Provider],
        order: [String],
        hidden: Set<String>
    ) -> [Provider] {
        let byID = Dictionary(uniqueKeysWithValues: providers.map { ($0.id, $0) })
        var seen = Set<String>()
        var result: [Provider] = []
        for id in order {
            guard !hidden.contains(id), let p = byID[id] else { continue }
            result.append(p)
            seen.insert(id)
        }
        for p in providers where !seen.contains(p.id) && !hidden.contains(p.id) {
            result.append(p)
        }
        return result
    }
}
