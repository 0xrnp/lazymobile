# Run your app

`lazymobile run` finds the device, starts it if it is off, and runs the app of the current folder on
it with the tool you already use. It shows the exact command, then hands the terminal over, so
`r` for hot reload and the other keys work as before.

```sh
lazymobile run                       # the device used here last time, else the one that is running
lazymobile run "Pixel 9a"            # start it if it is off, then run
lazymobile run "iPhone 17" -- --flavor dev     # flags after -- go to your tool
lazymobile run --here                # run it inside lazymobile, with its log beside it
lazymobile run --here --no-log       # the same, with the tool on the whole width (ctrl+p, then l, shows the log)
lazymobile run --split               # also open the dashboard in a pane beside this one
lazymobile run --choose              # ask again which way to run and which device
```

How it runs the app, the first that fits:

1. An action named `run` in `.lazymobile.json` (see [Project settings](../README.md#project-settings)). Its
   command is shown and needs `--yes`, because the file can come from a repository someone else
   wrote. `{device_id}` is replaced by the device:

   ```json
   {"actions": [{"name": "run", "command": ["make", "run-dev", "DEVICE={device_id}"]}]}
   ```

2. Your `package.json` scripts that run the app, such as `"ios": "expo run:ios"` or
   `"android": "react-native run-android"`. They run with your package manager (from the
   `packageManager` field, else the lock file, here or in a folder above), and the device is added
   to the script's own command, after `--` for npm. A platform no script covers, such as Android in
   a project with only an `ios` script, runs with the project's tool below.
3. Flutter (a `pubspec.yaml`): `flutter run -d <device>`.
4. Expo (`expo` in `package.json`): the project's own `expo run:ios` or `expo run:android` with
   `--device` (a simulator's UDID, an emulator's AVD name, or a phone's model). It uses the copy in `node_modules/.bin`, here or in a folder above for a monorepo,
   and downloads nothing.
5. React Native (`react-native` in `package.json`): the project's own `react-native run-ios --udid`
   or `run-android --device`.
6. A native Android app (a `gradlew`, and a module that applies `com.android.application`):
   `./gradlew :app:assembleDebug`, then `lazymobile install <device> app/build/outputs/apk/debug
   --launch`, which installs the APK and starts it under the id Gradle gave it. A project with
   product flavors names the one to build: `lazymobile run -- assembleDevDebug`. Android phones work
   too.
7. A native iOS app (an `.xcworkspace` or `.xcodeproj` in the folder or a folder in it, such as
   `iosApp`; one way to run per scheme):
   `xcodebuild -scheme <scheme> -configuration Debug -destination id=<simulator> build`, then
   `lazymobile install <simulator> <the built .app> --launch`. It builds where Xcode does
   (DerivedData), so a build made in Xcode is reused. Simulators only: an iPhone needs code
   signing, so run it from Xcode. The schemes come from the project's scheme files; a project with
   none is asked with `xcodebuild -list`, which takes a few seconds.

A folder with both, such as a Kotlin Multiplatform project, offers each on its own platform. The
build and install are one shell line, shown before it runs; to find where the app will be built,
lazymobile first asks `xcodebuild -showBuildSettings`, which can resolve the project's Swift
packages. `--here` shows the build in its pane and opens the app's log once it is launched.

When there are several ways, such as an `ios` and an `ios:release` script, or actions named
`run dev`, `run prod` and `run:staging`, it lists them once and remembers your answer for the folder. A device you name
leaves out the ways for the other platform. With no device named and more than one to choose from,
it lists the simulators and emulators (the ones running first) and remembers that too. A real phone
is listed only after the others, and is never remembered. `--profile <name>` picks a way without
asking. Without a terminal, such as in a script, it never asks: a plain `run` action is used, or it
stops and says to name one.

There are three ways to see your tool and the device together:

| | What you get | Where |
|---|---|---|
| `lazymobile run --here` | One window: your tool on the left, the app's log on the right, and the full dashboard a key away | Any terminal |
| `lazymobile run --split` | Your tool here, the dashboard in a new pane beside it | Rex, tmux, Zellij |
| `lazymobile run` and `lazymobile` | Your tool here, the dashboard in a second terminal you open | Any terminal |

## Inside lazymobile: `--here`

`lazymobile run --here` starts the dashboard on its run view. Your tool runs in the left pane on a
terminal of its own, so its colours, its QR code and its keys work as they do outside lazymobile.
The log of the app is on the right, and opens by itself once lazymobile knows the folder's app.

| Key | In the run view |
|---|---|
| any key | goes to your tool while its pane has the keys: `r` hot reload, `ctrl+c` stops it |
| `ctrl+]` | switch to the full dashboard (devices, logs, screenshots, bug report) and back; the tool keeps running |
| `ctrl+p` | the quick menu over the run view, from either pane: screenshot or recording to the clipboard, the log for AI, a bug report (see the key table above), and here also `l` to hide the log (the tool gets the whole width, `ctrl+o` shows the log by itself, and the folder remembers it) or show it again, and `+` and `-` to resize while the menu stays open. `--no-log` starts with the log hidden, for that run only |
| `ctrl+o` | give the keys to the log (filter `/`, level `L`, pause `p`, copy `y`) or back to the tool; `esc` goes back too |
| `l` | from the log, the first time: pick the app whose log to show |
| `shift+←`, `shift+→` | move the line between the panes, from either pane (the keys never reach your tool). Dragging the line with the mouse does too, and the folder remembers where it was left |
| `+`, `-` | the same, from the log |
| `z` | from the log: show it at full width, and again to show both |
| `shift+pgup`, `shift+pgdn`, the wheel | scroll back through the tool's output; typing goes back to the end |
| `enter` | after the tool ended: run it again |
| `q` | from the log, the dashboard or an ended tool: quit. While the tool runs it asks, then stops it (ctrl+c, then a kill after 5 seconds) so no dev server is left behind |

A click gives the keys to the pane clicked. Below 100 columns only one pane shows, the one with the
keys. lazymobile uses the mouse, so to select text with it, hold the key your terminal uses for
that, usually Shift (Option in some).

## More about `run`

- Keep the dashboard in a second terminal or split, in the same folder. When you start a run, it
  selects that device and opens the log of the app, so the tool's output and the device's log sit
  side by side. The first time it asks which app (press `l`, then pick it). After that it knows
  the app for the folder. A phone is selected, but its log is not opened for you. The dashboard
  only follows a run that began in the last 10 minutes and after you last used it.
- `--here` cannot be used with `--split`, and needs a terminal. It ends with exit code 0 when you
  quit: how the tool ended shows in its pane.
- `--split` opens that dashboard for you, in a new pane to the right of the terminal where you ran
  it, in the same folder, before your tool starts. It works in Rex, tmux and Zellij. In any other
  terminal it stops with a message and does not run your tool, so use a second terminal there. A
  tmux or Zellij inside a Rex terminal is split as tmux or Zellij.
- With no device named, it uses the simulator or emulator this folder was last run on, else the one
  that is running. With none or several running it asks you to name one. It never picks a real
  phone unless you name it, and never remembers one.
