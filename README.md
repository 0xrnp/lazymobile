<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="assets/logo-light.svg">
    <img src="assets/logo-light.svg" alt="lazymobile logo" width="120">
  </picture>
</p>

# lazymobile

[![ci](https://github.com/0xrnp/lazymobile/actions/workflows/ci.yml/badge.svg)](https://github.com/0xrnp/lazymobile/actions/workflows/ci.yml)

A terminal dashboard for mobile development: iOS simulators, Android emulators, connected phones,
their logs, and Expo builds, in one screen. It works in any terminal, and inside Neovim or
another editor's terminal.

![The dashboard following the log of a running Android emulator](docs/logs.png)

<details>
<summary>More screenshots</summary>

The device list:

![Simulators, emulators and a connected iPhone in one list](docs/devices.png)

The apps installed on the device, and the ones used before, offered when a log is opened with `l`:

![The apps used before, with version and build, offered before a log opens](docs/app-picker.png)

</details>

- One list for every simulator, emulator, iPhone and Android phone on the machine.
- Boot, cold boot, shut down, erase, create and delete simulators and emulators.
- Uninstall an app, with its data, from a simulator, emulator, iPhone or Android phone.
- Launch, restart or stop an app on a running device.
- Connect Android phones over Wi-Fi (pairing code or address).
- Live logs: filter by app, level and text. An Android app is followed across restarts. Open as
  many logs as you like, side by side or stacked, to read several devices at once.
- Inside an Expo project: list EAS builds, install a finished build on a running simulator or
  emulator, start and cancel builds.
- The same features as plain commands, with `--json`, for scripts and coding agents.
- `lazymobile doctor` says what is missing and how to install it.
- One binary. It calls the tools you already have (`xcrun`, `adb`, `emulator`, `eas`).

Requirements: macOS or Linux. iOS needs macOS with Xcode. Android needs the Android SDK (Android
Studio or the command-line tools). The builds pane needs the `eas` command.

## Status

Early release. What has been run, and what has not:

| | Tested |
| --- | --- |
| Unit tests | Full suite on macOS, with fake `xcrun`, `adb`, `emulator` and `eas`. CI runs it on Ubuntu and macOS |
| Listing devices, `doctor`, opening the dashboard | Real Xcode 27.2 and Android SDK on macOS |
| Android emulator boot, shut down and logs (`logcat`) | A real emulator on macOS |
| iOS simulator boot, shut down, installing an app, and both kinds of logs | A real simulator on Xcode 27.2, with a real React Native app |
| Erase, create and delete simulators | Fake tools only in this program. The same commands were run on real simulators and emulators in [devices.nvim](https://github.com/0xrnp/devices.nvim), which this logic is ported from |
| Expo builds pane | Listing, details, and downloading and installing Android and iOS simulator builds, with eas-cli 24.7 and a real Expo project. Starting and cancelling builds have only been run with a fake `eas` |
| iPhones | Listed over USB with a real iPhone, and so were its installed apps with their version and build, which also shows in the log heading. Its logs have only been tried with fake tools |
| App version in the log heading | A real Android emulator (Android 16), a real iPhone over USB and a real iOS simulator, with Xcode 27.2. Not run: Xcode 26 or older, other Android versions, Android phones, Wi-Fi iPhones |
| Several logs at once | Four real system logs of an iOS simulator, in a real terminal: a grid, stacked and one at a time, the list (`a`) closing one, and quitting and coming back with the rest restored. Also a simulator's log beside a real iPhone's launch-an-app log (the launch faked). Not run: more than four logs, or Android logs in several panes. The rest is covered by tests with fake tools |
| Uninstalling an app | A real iPhone that lazymobile listed as connected over Wi-Fi (it was first paired over USB), and Android, both run by the maintainer. The app list of a real simulator and refusing an app that is not installed were also run. Not run: a real removal on a simulator |
| Launching, restarting and stopping an app | A real iOS simulator (Xcode 27.2) and a real Android 16 emulator, from the commands. An app that is not installed was refused on both. Not run: an iPhone or an Android phone, other Xcode or Android versions, the dashboard key in a real terminal |
| Android phones | A real Android phone |
| `idevicesyslog` (an iPhone's system log) | Fake tools only |
| Linux | The dashboard on Linux, and a Homebrew install in a Linux container (arm64). The suite runs in CI on Ubuntu |
| Mouse and trackpad scrolling | A real terminal on macOS |

If something fails for you, please
[open an issue](https://github.com/0xrnp/lazymobile/issues/new/choose) with the output of
`lazymobile doctor`.

## Install

With [Homebrew](https://brew.sh), on macOS or Linux:

```sh
brew install 0xrnp/tap/lazymobile
```

With Go 1.26.7 or newer:

```sh
go install github.com/0xrnp/lazymobile/cmd/lazymobile@latest
```

Or download a binary for macOS or Linux from the
[releases page](https://github.com/0xrnp/lazymobile/releases), unpack it, and put `lazymobile` on
your `PATH`.

Then run `lazymobile doctor` to see what it found.

## The dashboard

Run `lazymobile`. Run it inside an Expo project to also get the builds pane.

Devices:

| Key | Action |
| --- | --- |
| `j` `k`, arrows | Move |
| `b` | Boot |
| `B` | Cold boot an emulator (ignore its snapshot) |
| `s` | Shut down |
| `l`, `enter` | Logs. Every log opens in a pane of its own, beside the ones that are open |
| `e` | Erase a simulator, or wipe an emulator's data. Asks first |
| `d` | Delete. Asks first |
| `o` | Launch, restart or stop an app: pick one, then pick what to do. An iPhone's apps can be launched and restarted, not stopped |
| `u` | Uninstall an app: lists the apps you installed (on an iPhone, its developer apps), then asks first |
| `n` | New simulator or emulator |
| `y` | Copy the id for `adb -s`, `flutter -d`, `xcrun` |
| `w` | Pair or connect an Android phone over Wi-Fi |
| `x` | Disconnect a Wi-Fi phone |
| `r` | Refresh |

Logs:

| Key | Action |
| --- | --- |
| `j` `k`, arrows | Scroll. The selected line is the one `enter` and `y` use |
| `pgup` `pgdn`, `ctrl+u` `ctrl+d`, `space` | Half a page up or down (`space` goes down) |
| `G` | Jump to the end and follow |
| `g` | First line |
| `enter` | Show the selected line in full, with the lines under it that belong to it, such as a stack trace |
| `y` | Copy the selected line, with the lines that belong to it |
| `h` `l`, `0` | Scroll sideways, reset |
| `w` | Wrap long lines, to read them in full |
| `/` | Filter by text (empty clears) |
| `L` | Minimum level |
| `p` | Pause or resume (nothing is lost) |
| `c` | Clear |
| `S` | Save what is shown to a file |
| `x` | Stop the stream |
| `X` | Close this log. The next one takes its place |
| `r` | Restart the stream |
| `v` | How the logs sit: side by side (the default), stacked, or one at a time |
| `a` | List all open logs: `enter` goes to one, `x` closes it |
| `1` to `9` | Go to that log |
| `z` | Show this log full screen. Again, or `esc`, to go back |

Builds (inside an Expo project):

| Key | Action |
| --- | --- |
| `enter` | Load the builds, then show a build's details |
| `i` | Download a finished build and install it on a running device |
| `o` | Open the build's page on expo.dev |
| `n` | Start a build. Shows the exact command and asks first |
| `x` | Cancel a build that is waiting or running. Asks first |
| `r` | Refresh |

Open as many logs as you like. Each has its own pane, filter, level, wrap and scroll position, and
its title says which it is ("Logs 2/5"). Side by side fills rows with as many columns as fit, stacked
fills columns with as many rows as fit, and a last row with fewer logs shares the whole width. When
the logs would be too small (under 30 columns by 6 rows each), or the screen is under 100 columns,
only the log in focus is shown. Then `tab` goes to the next log, `a` lists all of them, and `1` to
`9` jump straight to one. In the one-at-a-time layout, only the log in focus is shown beside the
devices, `tab` goes from log to log, and `esc` goes to the devices. `z` shows the focused log full
screen, and `tab` then goes from log to log.

Everywhere: `tab` moves to the next pane, `+` and `-` widen or narrow the devices pane, `z`
shows only the focused pane on the whole screen (again to undo), `?` opens the keys menu, `q` quits.
The keys menu lists what the keys do, those of the focused pane first. `/` filters it, `enter`
runs the action under the cursor (a question still asks first), and one that cannot be done now is
struck through, with the reason below. The mouse wheel scrolls the pane under it, and a click
focuses a pane and selects the device or build under it. Sliding sideways on a trackpad (or Shift
with the wheel) scrolls the log sideways. To select text with the
mouse, hold Option (macOS) or Shift while dragging.
Simulators and emulators keep running after you quit.

What each device offers for logs:

| Device | Logs |
| --- | --- |
| Android emulator or phone | `logcat`, optionally only one app (package name) |
| iOS simulator | System log, optionally one app (bundle id). Or launch the app and show its output |
| iPhone | Launch the app and show its output. Or the system log (needs `idevicesyslog`) |

When you give an Android package, the log keeps following the app when it restarts and gets a
new process id, and keeps crash reports and system messages that name it.

A release iOS app's `print()` and `NSLog` output goes to its standard output, which only the tool
that launched it sees. Choose "Launch an app and show its output" to see it.

When a log opens, lazymobile offers the app ids you used before and the apps installed on the
device, so there is nothing to type. On an iPhone, the list is the developer apps `devicectl`
reports, not the ones that come with iOS, and each shows its version and build.

The list starts with the apps you used before, marked `●`, when there are also others. `tab` shows
every installed app and back. `/` filters by text, matching the id, the name and the version, and
`enter` opens the first match. The version and build sit next to the id, so two builds of one app
can be told apart. An app you used before stays in the list even when the device does not have it, marked "not found
on this device", so you can use the same id on the next device, for a build that is still
installing. (Apps that come with the system are not in the device's list, so they are marked too.)
The uninstall list has the same filter. The apps are not sorted by when they
were installed, because the app lists lazymobile reads do not give an install time.

The log's heading shows the installed version and build of the app (`0.0.47 (37)`), on every kind
of device. It is read from the device each time the log starts, so after you install a new build,
restart the log with `r` to see it. In a narrow pane the heading drops the word "console", then the version, so that "paused" and "ended"
stay visible. Text matching the log filter is underlined.

### Where you left off

When you quit, lazymobile remembers, for the folder it was started in, the selected device, the
open logs, each with its filter, level and wrap, and how they sit. The
next start in that folder puts them back, and opens the log again if its device is still running.
A log that launches an app comes back too, marked "not started", and `r` launches the app:
starting it on its own would launch the app again. The lines themselves are not kept.

Everything remembered, including the recent app ids, is in
`~/.local/state/lazymobile/state.json` (or `$XDG_STATE_HOME/lazymobile/state.json`), readable
only by you. Delete the file to forget it all.

## Safety

- Erase, delete, wipe and uninstall always ask first. Real phones are never booted, shut down,
  erased or deleted. Uninstalling an app is the one change allowed on them, and only for an app
  you installed: the apps that come with the system are not listed and are refused.
- The builds pane does not run `eas` until you press `enter` in it. `eas` runs your project's
  own configuration code and uses your Expo login, so it is never started just by opening the
  dashboard.
- Starting a build shows the exact command and asks first, because it uses build time on your
  Expo account. A profile with "prod" in its name must be typed out to confirm.
- Log lines are stripped of terminal control sequences before they are drawn.
- Saved logs are readable only by you, since logs can hold tokens and personal data.

## Commands

```sh
lazymobile list [--json]
lazymobile boot "Pixel 9a" --cold --wait
lazymobile boot "Pixel 9a" --wipe --yes
lazymobile boot "iPhone 17" --no-window
lazymobile shutdown "Pixel 9a"
lazymobile shutdown "iPhone 17" --keep-simulator-app
lazymobile erase "iPhone 17" --yes
lazymobile delete "iPhone 17" --yes
lazymobile uninstall "Pixel 9a" com.example.app --yes   # the app and its data
lazymobile install "Pixel 9a" ./app.apk
lazymobile launch "Pixel 9a" com.example.app [--restart]
lazymobile stop "Pixel 9a" com.example.app   # keeps the app's data
lazymobile logs "Pixel 9a" --app com.example.app --level warning
lazymobile logs "iPhone 17" --app com.example.app --mode console --json
lazymobile logs "iPhone 17" --last-app   # the app id used most recently on iOS
lazymobile apps "iPhone 17" [--json]     # app ids used before, and the apps installed on it
lazymobile id "Pixel 9a"          # emulator-5554 when running
lazymobile builds [--json] [--limit 20]
lazymobile doctor [--json]
```

A device is an id, an adb serial, a name, or a unique part of a name. Commands that erase,
delete or uninstall need `--yes`. Exit codes: 0 success, 1 failure, 2 wrong usage.

App ids given to `logs` and `launch` are remembered, newest first, and shared with the dashboard.

`logs --json` prints one object per line (`text`, `level` from 1 verbose to 6 fatal, `pid`), which
is easy for a script or a coding agent to read.

## Project settings

Optional. A `.lazymobile.json` next to `eas.json` changes how `eas` is run:

```json
{
  "eas": {
    "command": ["pnpm", "exec", "eas"],
    "build_command": ["make", "eas-build-{profile}", "PLATFORM={platform}"]
  }
}
```

- `command`: how to run the eas CLI. By default the project's own copy
  (`node_modules/.bin/eas`) is used, then `eas` from your `PATH`.
- `build_command`: what starts a build, in place of `eas build`. Use it when your project starts
  builds through its own script, for example one that checks a release gate first. `{profile}`
  and `{platform}` are replaced.

The project is found by looking for `eas.json` in the current folder and its parents, up to the
repository root, and then in `apps/*/`.

## In Neovim

This repository is also a Neovim plugin (0.10 or newer). It opens lazymobile in a floating
terminal, the way lazygit.nvim does. With lazy.nvim:

```lua
{
  "0xrnp/lazymobile",
  cmd = "Lazymobile",
  keys = { { "<leader>m", "<cmd>Lazymobile<cr>", desc = "lazymobile" } },
  opts = {}, -- optional: { cmd = { "lazymobile", "--keep-simulator-app" }, width = 0.9, height = 0.9, border = "rounded", toggle_key = "<C-q>" }
}
```

The plugin only opens the program, so install the `lazymobile` binary as well (see Install).

`:Lazymobile` opens it in Neovim's current folder, so inside an Expo project the builds pane is
there.

- `<C-q>` inside the window hides it while lazymobile keeps running, so a log keeps streaming.
  `:Lazymobile` (or your key) brings it back as it was. Set `toggle_key` to change the key, or to
  `false` for none.
- `q` quits lazymobile and closes the window. The next start in the same folder puts back the
  device and the log you had open (see "Where you left off").
- If lazymobile cannot start, the window stays open with its message.

If you map `<Esc>` in terminal mode, lazymobile does not receive it. Its windows also close with
`ctrl+c`.

## Troubleshooting

Run `lazymobile doctor` first.

- **No Android devices**: the SDK was not found. Set `ANDROID_HOME`.
- **Phone shows "unauthorized"**: unlock it and accept the USB debugging prompt.
- **Linux: "no permissions"**: install udev rules for Android devices (`android-udev-rules`).
- **Simulators stop when DeviceHub quits (Xcode 27)**: that is DeviceHub's "Stop simulators on
  quit" setting. Turn it off in DeviceHub's settings.
- **Simulator or DeviceHub quits after a simulator shuts down**: once no simulator is running,
  lazymobile quits them, which closes the window they keep open for a shut-down simulator. macOS
  asks once whether your terminal may control them. To keep them open, start lazymobile with
  `--keep-simulator-app` (the dashboard and `shutdown` both take it).
- **iPhone shows "disconnected"**: connect it once by cable, trust the computer, and turn on
  Developer Mode in Settings > Privacy & Security.
- **Creating an Android emulator fails about Java**: install a JDK 17 or newer, or set
  `JAVA_HOME`.
- **Builds pane says you are not logged in**: run `eas login`.
- **Builds pane says the app config could not be loaded**: `eas` never reads `.env` files. If
  your `app.config` requires variables from `.env`, set them in your shell, or put them in the
  command in `.lazymobile.json`, for example
  `["env", "EXPO_PUBLIC_API_URL=https://example.com", "npx", "eas"]`.
- **Copy does nothing**: copying uses the terminal's clipboard support (OSC 52). In tmux, turn on
  `set -g set-clipboard on`.

## Development

```sh
make check    # formatting, vet, and the tests with the race detector
make build    # ./lazymobile
```

The tests use fake tools, so they run anywhere and never touch a real device or an Expo account.
A fix for a parsing or tool-output problem should come with a test built from the real output,
with names and ids replaced.

The name follows the convention of lazygit and lazydocker. This project is not affiliated with
them.

Changes are welcome: see [CONTRIBUTING.md](CONTRIBUTING.md). Security problems go to
[SECURITY.md](SECURITY.md).

## License

MIT
