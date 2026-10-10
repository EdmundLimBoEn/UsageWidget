import AppIntents

struct ProviderWidgetIntent: WidgetConfigurationIntent {
    static let title: LocalizedStringResource = "Capacity providers"
    static let description = IntentDescription("Follow your app's provider preferences or choose providers for this widget.")

    @Parameter(title: "Use app providers", default: true)
    var useAppProviders: Bool

    @Parameter(title: "Providers")
    var providers: [WidgetProviderEntity]?

    static var parameterSummary: some ParameterSummary {
        When(\.$useAppProviders, .equalTo, true) {
            Summary { \.$useAppProviders }
        } otherwise: {
            Summary {
                \.$useAppProviders
                \.$providers
            }
        }
    }

    var selectedIDs: [String]? {
        useAppProviders ? nil : (providers ?? []).map(\.id)
    }
}

struct WidgetProviderEntity: AppEntity {
    static let typeDisplayRepresentation: TypeDisplayRepresentation = "Provider"
    static let defaultQuery = WidgetProviderQuery()

    let id: String
    let name: String

    var displayRepresentation: DisplayRepresentation {
        DisplayRepresentation(title: "\(name)")
    }
}

struct WidgetProviderQuery: EntityQuery {
    var store: SnapshotStore = .shared

    func entities(for identifiers: [String]) async throws -> [WidgetProviderEntity] {
        let cached = store.loadSnapshot()?.providers ?? []
        // Keep saved identifiers resolvable when a provider temporarily disappears from the snapshot.
        return identifiers.map { id in
            WidgetProviderEntity(id: id, name: cached.first(where: { $0.id == id })?.name ?? id)
        }
    }

    func suggestedEntities() async throws -> [WidgetProviderEntity] {
        return WidgetProviderSelection.providers(
            from: store.loadSnapshot()?.providers ?? [],
            preferences: store.loadPreferences()
        ).map { WidgetProviderEntity(id: $0.id, name: $0.name) }
    }
}
