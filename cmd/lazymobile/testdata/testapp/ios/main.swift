// The app the device tests install on a simulator. It logs when it starts, when it opens a link
// and when a push notification arrives, and crashes on lazymobilee2e://crash so that crash reports
// can be read back. lazymobilee2e://write?value=X (or the launch argument --write X) saves X as a preference and as a
// file in Documents, and lazymobilee2e://read (or --read) logs what is saved, so that clearing an app's data can be checked.
import UIKit
import UserNotifications

private func e2eFile() -> URL {
    FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0].appendingPathComponent("e2e.txt")
}

func saveE2E(_ value: String) {
    UserDefaults.standard.set(value, forKey: "e2e_value")
    UserDefaults.standard.synchronize()
    try? value.write(to: e2eFile(), atomically: true, encoding: .utf8)
    NSLog("LAZYMOBILE_E2E wrote %@", value)
}

func readE2E() {
    let preference = UserDefaults.standard.string(forKey: "e2e_value") ?? "none"
    let saved = (try? String(contentsOf: e2eFile(), encoding: .utf8)) ?? "none"
    NSLog("LAZYMOBILE_E2E saved preference=%@ file=%@", preference, saved)
}

class AppDelegate: UIResponder, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    func application(_ application: UIApplication, didFinishLaunchingWithOptions options: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        NSLog("LAZYMOBILE_E2E launched")
        // The same two actions as the links, for a launch with arguments, which needs no "Open in" tap.
        let arguments = CommandLine.arguments
        if let at = arguments.firstIndex(of: "--write"), at + 1 < arguments.count { saveE2E(arguments[at + 1]) }
        if arguments.contains("--read") { readE2E() }
        if let at = arguments.firstIndex(of: "--crash"), at + 1 < arguments.count {
            fatalError("LAZYMOBILE_E2E crash on purpose: \(arguments[at + 1])")
        }
        // Provisional permission is granted without a prompt, so a push can arrive with no one to tap Allow.
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        center.requestAuthorization(options: [.alert, .sound, .provisional]) { granted, _ in
            NSLog("LAZYMOBILE_E2E notifications %@", granted ? "allowed" : "refused")
        }
        return true
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification, withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        NSLog("LAZYMOBILE_E2E push %@", notification.request.content.body)
        done([.banner])
    }
}

class SceneDelegate: UIResponder, UIWindowSceneDelegate {
    var window: UIWindow?

    func scene(_ scene: UIScene, willConnectTo session: UISceneSession, options: UIScene.ConnectionOptions) {
        guard let scene = scene as? UIWindowScene else { return }
        let controller = UIViewController()
        controller.view.backgroundColor = .systemTeal
        let label = UILabel(frame: controller.view.bounds)
        label.text = "lazymobile e2e"
        label.textAlignment = .center
        controller.view.addSubview(label)
        window = UIWindow(windowScene: scene)
        window?.rootViewController = controller
        window?.makeKeyAndVisible()
        handle(options.urlContexts)
    }

    func scene(_ scene: UIScene, openURLContexts contexts: Set<UIOpenURLContext>) {
        handle(contexts)
    }

    private func handle(_ contexts: Set<UIOpenURLContext>) {
        for context in contexts {
            NSLog("LAZYMOBILE_E2E opened %@", context.url.absoluteString)
            let value = URLComponents(url: context.url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "value" }?.value ?? ""
            if context.url.host == "write" { saveE2E(value) }
            if context.url.host == "read" { readE2E() }
            if context.url.host == "crash" { fatalError("LAZYMOBILE_E2E crash on purpose: \(context.url.absoluteString)") }
        }
    }
}

UIApplicationMain(CommandLine.argc, CommandLine.unsafeArgv, nil, NSStringFromClass(AppDelegate.self))
