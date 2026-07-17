import SwiftUI

struct SetupView: View {
    @Environment(AppModel.self) private var model
    @State private var serverURL: String = ""
    @State private var token: String = ""
    @State private var isTesting = false
    @State private var statusText: String?
    @State private var statusOK = false

    var body: some View {
        Form {
            Section {
                TextField("https://edserve.ts.net/usagewidget", text: $serverURL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
                SecureField("Bearer token", text: $token)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            } header: {
                Text("edServe connection")
            } footer: {
                Text("Use the Tailscale HTTPS URL and the USAGEWIDGET_TOKEN from the server env file. CodexBar credentials never leave the Linux host.")
            }

            Section {
                Button {
                    Task { await testAndSave() }
                } label: {
                    if isTesting {
                        ProgressView()
                    } else {
                        Text(model.isConfigured ? "Save & retest" : "Test connection")
                    }
                }
                .disabled(serverURL.isEmpty || token.isEmpty || isTesting)

                if let statusText {
                    Label(statusText, systemImage: statusOK ? "checkmark.circle.fill" : "xmark.circle.fill")
                        .foregroundStyle(statusOK ? .green : .red)
                }
            }
        }
        .navigationTitle("Connect")
        .onAppear {
            if let creds = model.credentials {
                serverURL = creds.serverURL
                token = creds.token
            }
        }
    }

    private func testAndSave() async {
        isTesting = true
        defer { isTesting = false }
        do {
            try await model.saveConnection(url: serverURL, token: token)
            statusOK = true
            if let health = model.health {
                statusText = "OK — codexbar=\(health.codexbar) db=\(health.database) polling=\(health.polling) apns=\(health.apns)"
            } else {
                statusText = "Connected"
            }
        } catch {
            statusOK = false
            statusText = String(describing: error)
        }
    }
}
