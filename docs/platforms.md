# What works where

The same commands work on iOS and Android wherever the platform allows it. Where a cell says
"no", the reason is in the notes below the table. Each of these was checked against the code, and
the ones marked "run" were run on a real simulator or emulator (see [tested.md](tested.md)).

| | iOS simulator | iPhone | Android emulator | Android phone |
| --- | --- | --- | --- | --- |
| List, boot, shut down | yes | list only | yes | list only |
| Create, erase, delete | yes | no | yes | no |
| Install, launch, stop an app | yes | install, launch | yes | yes |
| Uninstall an app | yes | yes | yes | yes |
| Logs, with crashes marked | yes | yes (`idevicesyslog`) | yes | yes |
| Crash report | yes | yes | yes | yes |
| Screenshot | yes | yes ([4](#4-the-iphone)) | yes | yes |
| Screen recording | yes | built, needs an iOS that offers it ([4](#4-the-iphone)) | yes | yes |
| Open a link | yes ([1](#1-deep-links-on-a-simulator)) | yes, in a named app ([4](#4-the-iphone)) | yes | yes |
| Dark and light mode | yes | yes | yes | yes |
| Location, status bar | yes | location yes, status bar built but refused by iOS 27.0 ([4](#4-the-iphone)) | yes | no ([8](#8-android-phones)) |
| Grant or revoke a permission | yes | no ([4](#4-the-iphone)) | yes | no ([8](#8-android-phones)) |
| Clear an app's data | yes ([2](#2-clearing-data-on-a-simulator)) | no ([4](#4-the-iphone)) | yes | no ([8](#8-android-phones)) |
| Test push notification | yes | no | no ([3](#3-push-on-android)) | no |
| CPU and memory of an app | yes | no ([4](#4-the-iphone)) | yes | yes |
| Janky frames | no ([5](#5-jank-on-ios)) | no | yes | yes |
| Offline or slow network | no ([6](#6-network-on-ios)) | no | yes | no |
| Metro and developer menu helpers | no ([7](#7-metro-helpers)) | no | yes | yes |
| Add photos and videos | yes | no | yes | no ([8](#8-android-phones)) |
| Set the clipboard | yes | no | no ([9](#9-the-android-clipboard)) | no |
| Read the screen, tap, type, swipe | no ([10](#10-touching-an-ios-simulator)) | no | yes | no ([8](#8-android-phones)) |
| `create` | yes | no | yes | no |

## Notes

### 1. Deep links on a simulator

Xcode 27's simulator asks "Open in …?" before it opens a deep link (a custom scheme such as
`myapp://home`), and waits for a tap in the Simulator window. `lazymobile link` and `open_link` say
so after they open one. A web address opens at once. Nothing in lazymobile can tap, and Apple's
tools have no option to skip the question.

### 2. Clearing data on a simulator

Apple has no command for it, so lazymobile stops the app and empties its data folder (Documents,
Library with its preferences and caches, tmp). Its app group folders, which apps of the same
developer share, and its keychain items are kept. Run on a simulator with an app that saved a
preference and a file: both were gone afterwards, also when the app was running.

### 3. Push on Android

A push on Android comes through Firebase Cloud Messaging, from Google's servers to the app's own
registration. There is no local command that delivers one to an app, so lazymobile does not pretend
to. For a notification test on Android, send a message from the Firebase console.

### 4. The iPhone

Xcode 27's `devicectl` can do more on a real iPhone than lazymobile uses. A screenshot is built and
was run on an iPhone 17 Pro (a valid PNG, saved so only you can read it). Screen recording is built
too, and on that phone, which runs iOS 27.0, `devicectl` answered "The capability Screen Recording is
not supported by this device", so lazymobile says so and points to QuickTime Player; a newer iOS may
offer it. A phone is a person's own, so what changes it is kept to what can be undone, and each change prints the
command that undoes it. Dark and light mode (`settings appearance`), a simulated location
(`simulate location`), a tidy status bar (`simulate statusBar preset screenshot`) and a link
(`process launch --payload-url`) are built. A phone has no command to ask which app handles a link, so
a web address goes to Safari and anything else needs `--app <bundle id>`; the app is launched. The
agent tools refuse a real phone unless they are called with `physical` true, for when the user named
it. Run on an iPhone 17 Pro with iOS 27.0: dark and light mode, a location (and clearing it) and a link in Safari worked, and the status bar answered "The capability Simulate Status Bar is not supported by this device", which lazymobile reports with that reason; a newer iOS may offer it. Face ID, which `devicectl` can simulate too, is left out on purpose: a simulated sign-in on a
personal phone is not worth the risk. An iPhone's CPU and
memory, and its app permissions and data, have no `devicectl` command that was found (Instruments
measures CPU and memory).

### 5. Jank on iOS

Instruments has an Animation Hitches template, and `xctrace` ran it on a simulator: it answered
"Hitches is not supported on this platform". It is meant for a real iPhone, which was not tried when
this was written, so it is not built. Android uses `dumpsys gfxinfo`.

### 6. Network on iOS

A simulator uses the Mac's network. Slowing that down needs Apple's Network Link Conditioner, which
changes the whole Mac and needs an administrator, so lazymobile does not touch it. An Android
emulator has its own console for speed and delay, and airplane mode for no connection.

### 7. Metro helpers

`lazymobile dev` forwards a port with `adb reverse`, which an iOS simulator does not need because it
shares the Mac's localhost. No command-line route to its developer menu or reload was found for iOS.

### 8. Android phones

An emulator can be told where it is and given a demo status bar through its console. A phone has no
such console, and its permissions and data are left alone like an iPhone's. Dark and light mode,
screenshots, recordings, links, CPU, memory and jank work on a phone.

### 9. The Android clipboard

Android has no shell command to set the clipboard: `cmd clipboard` answers "No shell command
implementation" (tried on an Android 16 emulator). Only an app on the device can write it, and
lazymobile does not install one. `adb shell input text` types into the focused field, which is a
different thing.

### 10. Touching an iOS simulator

`xcrun simctl` has no command to tap, type, swipe or read the screen's elements (its `io` and `ui`
commands cover screenshots, recordings and the appearance only). An iOS simulator is driven with
XCUITest, or with a tool built on it such as Maestro. lazymobile does not wrap those: `ui_tree`,
`tap`, `swipe`, `type_text` and `press_key` say so on a simulator. An Android emulator uses
`adb shell input` and `uiautomator`. A phone's screen is the person's own, so it is not touched.
