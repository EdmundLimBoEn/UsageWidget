import Foundation
import Security

public enum APIError: Error, Equatable {
    case invalidBaseURL
    case invalidResponse
    case httpStatus(Int, String?)
    case decoding(String)
    case transport(String)
}

public struct APIClient: Sendable {
    public var baseURL: URL
    public var token: String
    public var session: URLSession
    public var timeout: TimeInterval

    public init(
        baseURL: URL,
        token: String,
        session: URLSession = .shared,
        timeout: TimeInterval = 20
    ) {
        self.baseURL = baseURL
        self.token = token
        self.session = session
        self.timeout = timeout
    }

    public static func make(credentials: ConnectionCredentials, session: URLSession = .shared, timeout: TimeInterval = 20) throws -> APIClient {
        guard let url = normalizedBaseURL(credentials.serverURL) else {
            throw APIError.invalidBaseURL
        }
        return APIClient(baseURL: url, token: credentials.token, session: session, timeout: timeout)
    }

    public static func normalizedBaseURL(_ string: String) -> URL? {
        var s = string.trimmingCharacters(in: .whitespacesAndNewlines)
        while s.hasSuffix("/") { s.removeLast() }
        return URL(string: s)
    }

    public func fetchSnapshot() async throws -> Snapshot {
        try await get(path: "/v1/snapshot")
    }

    public func fetchHealth() async throws -> Health {
        try await get(path: "/v1/health")
    }

    public func fetchSettings() async throws -> ServerSettings {
        try await get(path: "/v1/settings")
    }

    public func updateSettings(_ settings: ServerSettings) async throws -> ServerSettings {
        try await send(path: "/v1/settings", method: "PUT", body: settings)
    }

    public func registerDevice(_ device: DeviceRegistration) async throws -> DeviceRegistration {
        try await send(path: "/v1/devices", method: "POST", body: device)
    }

    public func forcePoll() async throws -> PollResult {
        var request = try makeRequest(path: "/v1/poll", method: "POST")
        // Collection can take up to 250 seconds on the backend.
        request.timeoutInterval = max(timeout, 270)
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            throw APIError.transport(error.localizedDescription)
        }
        guard let http = response as? HTTPURLResponse else { throw APIError.invalidResponse }
        guard (200..<300).contains(http.statusCode) || http.statusCode == 502 else {
            throw APIError.httpStatus(http.statusCode, String(data: data, encoding: .utf8))
        }
        do {
            return try JSONCoding.decoder.decode(PollResult.self, from: data)
        } catch {
            throw APIError.decoding(String(describing: error))
        }
    }

    public func fetchReadiness(deviceID: String) async throws -> Readiness {
        try await get(path: "/v1/readiness/\(deviceID)")
    }

    public func testReadiness(deviceID: String) async throws -> ReadinessTestResult {
        try await sendEmpty(path: "/v1/readiness/\(deviceID)/test", method: "POST")
    }

    private func get<T: Decodable>(path: String) async throws -> T {
        let request = try makeRequest(path: path, method: "GET")
        return try await perform(request)
    }

    private func sendEmpty<T: Decodable>(path: String, method: String) async throws -> T {
        let request = try makeRequest(path: path, method: method)
        return try await perform(request)
    }

    private func send<Body: Encodable, T: Decodable>(path: String, method: String, body: Body) async throws -> T {
        var request = try makeRequest(path: path, method: method)
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONCoding.encoder.encode(body)
        return try await perform(request)
    }

    public func makeRequest(path: String, method: String) throws -> URLRequest {
        guard let url = Self.resolve(base: baseURL, path: path) else {
            throw APIError.invalidBaseURL
        }
        var request = URLRequest(url: url, timeoutInterval: timeout)
        request.httpMethod = method
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        return request
    }

    /// Appends `path` onto the base (preserving any path prefix like `/usagewidget`).
    public static func resolve(base: URL, path: String) -> URL? {
        let trimmed = path.hasPrefix("/") ? String(path.dropFirst()) : path
        var baseString = base.absoluteString
        while baseString.hasSuffix("/") {
            baseString.removeLast()
        }
        return URL(string: baseString + "/" + trimmed)
    }

    private func perform<T: Decodable>(_ request: URLRequest) async throws -> T {
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            throw APIError.transport(error.localizedDescription)
        }
        guard let http = response as? HTTPURLResponse else {
            throw APIError.invalidResponse
        }
        guard (200..<300).contains(http.statusCode) else {
            let msg = String(data: data, encoding: .utf8)
            throw APIError.httpStatus(http.statusCode, msg)
        }
        do {
            return try JSONCoding.decoder.decode(T.self, from: data)
        } catch {
            throw APIError.decoding(String(describing: error))
        }
    }
}

/// Short, user-facing text for errors shown in the widget (and anywhere else a
/// raw `String(describing: error)` would leak enum cases, HTTP bodies, or
/// URLSession jargon). Keep every message short enough for one widget line.
public enum FriendlyError {
    public static let notSetUp = "Not set up"
    public static let cantReachServer = "Can't reach server"
    public static let tokenRejected = "Token rejected"
    public static let wrongServerPath = "Server path not found"
    public static let unexpectedResponse = "Unexpected server response"
    public static let unlockToRefresh = "Unlock iPhone to refresh"
    public static let generic = "Couldn't refresh"

    public static func serverError(_ status: Int) -> String { "Server error (\(status))" }

    public static func message(for error: Error) -> String {
        switch error {
        case let api as APIError:
            return message(for: api)
        case let keychain as KeychainError:
            switch keychain {
            case .unexpectedStatus(errSecInteractionNotAllowed):
                return unlockToRefresh
            case .unexpectedStatus, .missingValue:
                return notSetUp
            }
        case is URLError:
            return cantReachServer
        case is DecodingError:
            return unexpectedResponse
        default:
            return generic
        }
    }

    private static func message(for error: APIError) -> String {
        switch error {
        case .invalidBaseURL:
            return notSetUp
        case .transport:
            return cantReachServer
        case let .httpStatus(status, _):
            switch status {
            case 401, 403: return tokenRejected
            case 404: return wrongServerPath
            // Cloudflare/tunnel origin errors mean the server itself is unreachable.
            case 502, 503, 504, 521, 522, 523, 530: return cantReachServer
            default: return serverError(status)
            }
        case .invalidResponse, .decoding:
            return unexpectedResponse
        }
    }
}
