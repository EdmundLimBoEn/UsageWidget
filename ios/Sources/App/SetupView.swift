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
            if !model.isConfigured {
                Section {
                    Button {
                        model.enterSamplePreview()
                    } label: {
                        Label("Preview with sample data", systemImage: "eye")
                    }
                    .accessibilityHint("Opens the dashboard with example capacity. This is not your usage.")
                } footer: {
                    Text("See remaining capacity, reset times, and alerts using sample data. This is not connected to a server and is not your usage.")
                }
            }

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
                Text("Scan the installer QR, or paste the Tailscale HTTPS URL and bearer token. Provider logins stay on the machine that collects usage.")
            }

            Section {
                Button {
                    if DataScannerViewController.isSupported && DataScannerViewController.isAvailable {
                        showScanner = true
                    } else {
                        statusOK = false; statusText = "QR scanning is unavailable. Check camera permission or use manual entry."
                    }
                } label: { Label("Scan QR", systemImage: "qrcode.viewfinder") }

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

            Section {
                Link(destination: AppConstants.privacyPolicyURL) {
                    Label("Privacy Policy", systemImage: "hand.raised")
                }
                Link(destination: AppConstants.supportURL) {
                    Label("Support", systemImage: "questionmark.circle")
                }
            } footer: {
                Text(AppConstants.affiliationDisclaimer)
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
            if let health = model.health, !health.upstreamOK {
                statusText = "Connected, but the collector is not returning usage yet."
            } else {
                statusText = "Connected"
            }
        } catch {
            statusOK = false
            statusText = String(describing: error)
        }
    }
}
