import SwiftUI
import UIKit
import UserNotifications
import WidgetKit

@main
struct UsageWidgetApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @State private var model = AppModel()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(model)
                .onAppear {
                    appDelegate.model = model
                    Task {
                        await model.refresh()
                        await model.registerTokensIfNeeded()
                    }
                }
        }
    }
}

struct RootView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        Group {
            if model.isConfigured {
                TabView {
                    NavigationStack {
                        DashboardView()
                    }
                    .tabItem { Label("Dashboard", systemImage: "chart.bar.fill") }

                    NavigationStack {
                        SettingsView()
                    }
                    .tabItem { Label("Settings", systemImage: "gearshape") }
                }
            } else {
                NavigationStack {
                    SetupView()
                }
            }
        }
    }
}

final class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    weak var model: AppModel?

    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        return true
    }

    func application(
        _ application: UIApplication,
        didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data
    ) {
        let hex = deviceToken.map { String(format: "%02x", $0) }.joined()
        Task { @MainActor in
            await model?.registerTokensIfNeeded(apnsToken: hex)
        }
    }

    func application(
        _ application: UIApplication,
        didFailToRegisterForRemoteNotificationsWithError error: Error
    ) {
        Task { @MainActor in
            model?.notificationStatus = "registration failed"
            model?.errorMessage = error.localizedDescription
        }
    }
}
