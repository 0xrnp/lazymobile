// The app the device tests install on a simulator. It logs when it starts and when it opens a
// link, and crashes on lazymobilee2e://crash so that crash reports can be read back.
import UIKit

class AppDelegate: UIResponder, UIApplicationDelegate {
    func application(_ application: UIApplication, didFinishLaunchingWithOptions options: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        NSLog("LAZYMOBILE_E2E launched")
        return true
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
