import Foundation

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

    public func deleteDevice(id: String) async throws {
        let request = try makeRequest(path: "/v1/devices/\(id)", method: "DELETE")
        let (_, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw APIError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            throw APIError.httpStatus(http.statusCode, nil)
        }
    }

    // MARK: - Internals

    private func get<T: Decodable>(path: String) async throws -> T {
        let request = try makeRequest(path: path, method: "GET")
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
