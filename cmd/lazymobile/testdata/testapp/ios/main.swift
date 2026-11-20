// The app the device tests install on a simulator. It logs when it starts, when it opens a link
// and when a push notification arrives, and crashes on lazymobilee2e://crash so that crash reports
// can be read back.
import UIKit
import UserNotifications

class AppDelegate: UIResponder, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    func application(_ application: UIApplication, didFinishLaunchingWithOptions options: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        NSLog("LAZYMOBILE_E2E launched")
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
            if context.url.host == "crash" { fatalError("LAZYMOBILE_E2E crash on purpose: \(context.url.absoluteString)") }
        }
    }
}

UIApplicationMain(CommandLine.argc, CommandLine.unsafeArgv, nil, NSStringFromClass(AppDelegate.self))
