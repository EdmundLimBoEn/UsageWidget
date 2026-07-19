import SwiftUI
import VisionKit

struct SetupView: View {
    @Environment(AppModel.self) private var model
    @State private var serverURL: String = ""
    @State private var token: String = ""
    @State private var isTesting = false
    @State private var statusText: String?
    @State private var statusOK = false
    @State private var showScanner = false
    @State private var pendingQR: QRConfiguration?
    @State private var showQRConfirmation = false

    var body: some View {
        Form {
            Section {
                TextField("https://your-host.your-tailnet.ts.net/usagewidget", text: $serverURL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
                SecureField("Bearer token", text: $token)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            } header: {
                Text("Server connection")
            } footer: {
                Text("Use the Tailscale HTTPS URL and the USAGEWIDGET_TOKEN from the server env file. CodexBar credentials never leave the Linux host.")
            }

            Section {
                Button {
                    if DataScannerViewController.isSupported && DataScannerViewController.isAvailable {
                        showScanner = true
                    } else {
                        statusOK = false; statusText = "QR scanning is unavailable. Check camera permission or use manual entry."
                    }
                } label: { Label("Scan installer QR", systemImage: "qrcode.viewfinder") }

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
        .fullScreenCover(isPresented: $showScanner) {
            NavigationStack {
                QRScannerView { payload in
                    showScanner = false
                    do {
                        pendingQR = try QRConfiguration.parse(payload)
                        showQRConfirmation = true
                    } catch {
                        statusOK = false; statusText = "Invalid UsageWidget QR code."
                    }
                }
                .ignoresSafeArea()
                .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { showScanner = false } } }
            }
        }
        .alert("Connect to this server?", isPresented: $showQRConfirmation, presenting: pendingQR) { configuration in
            Button("Cancel", role: .cancel) { pendingQR = nil }
            Button("Test & Save") {
                serverURL = configuration.serverURL; token = configuration.token
                Task { await testAndSave() }
            }
        } message: { configuration in
            Text(URL(string: configuration.serverURL)?.host ?? configuration.serverURL)
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
