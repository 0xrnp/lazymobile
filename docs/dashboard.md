# The dashboard

Run `lazymobile`. Run it inside an Expo project to also get the builds pane. `?` in the dashboard
lists every key, those of the focused pane first, and runs the one you pick.

## Keys

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
| `U` | Delete the simulators Xcode can no longer run, such as after an update removed their runtime. The pane says how many there are and how much disk they use. It names them and asks first, then runs `xcrun simctl delete unavailable` once. A simulator that can run is never touched |
| `o` | Pick an app, then what to do with it: launch, restart or stop it, show its latest crash report, or save a bug report (a new folder here, as `lazymobile report` makes). On a simulator or emulator also grant or revoke a permission, on an emulator clear its data (asks first), and on a simulator send a test push notification. An iPhone's apps can be launched and restarted, not stopped, and their latest crash report is copied from the phone. An iPhone has no bug report in this menu |
| `f` | Put a simulator or emulator at a place (latitude,longitude). On a simulator, `clear` takes it off. Not for real phones |
| `A` | Add a photo or video to a simulator or emulator: it asks for a path on this computer. Not for real phones |
| `V` | Copy text to an iOS simulator's clipboard. Not for Android |
| `N` | Slow down or cut off an Android emulator's network: no connection, GSM, EDGE, 3G, LTE, or back to normal. Emulators only |
| `c` `C` | Tidy the status bar of a simulator or emulator for a screenshot (9:41, full battery and signal), or restore it. Not for real phones |
| `m` | Switch between dark and light mode. An Android device that follows the time of day is put in dark mode. Not for an iPhone |
| `p` | Save a screenshot, a PNG, in the folder lazymobile was started in. An iPhone too, through Xcode's `devicectl`, while it is unlocked |
| `ctrl+p` | The quick menu, a letter each: `s` a screenshot, saved and copied as a picture to paste into a chat; `r` start or stop a screen recording, saved and copied as a file to attach; `c` the newest 200 lines of the open log, as the pane shows them (its filter and level apply), copied as text to ask an AI what went wrong; `b` a bug report; `o` the app menu; `t` a link; `m` dark mode. It works in the run view too, from either pane, on the run's device. A recording shows `● REC` and a time, and quitting while one runs asks, then saves the video first (it is saved, not copied). On Android a recording stops by itself after 3 minutes, and is saved and copied then. Needs `pbcopy` and `osascript` on macOS, and `wl-copy` or `xclip` on Linux, in a desktop session. Without one, a screenshot or video is still saved and the note says it could not be copied, and `c` asks the terminal to copy instead, which not every terminal does |
| `t` | Open a web address or deep link, such as `myapp://home?id=1`. Not for an iPhone |
| `D` | For an app in development on an Android device: reach this computer on port 8081 (Metro), open the app's developer menu, or reload it |
| `R` | Run one of the project's own commands from `.lazymobile.json` on the selected running device. It shows the exact command and the folder, and asks first. The command takes over the terminal until it ends |
| `i` | Install an `.apk` or `.app` on a running device: pick a build found in the folder (Gradle, Flutter and Xcode build folders) or one used before, or type a path. An iPhone needs a build made and signed for devices |
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
| `e` `E` | Jump to the next or previous error. A crash, or a run of error lines such as a stack trace, is one stop |
| `g` | First line |
| `enter` | Show the selected line in full, with the lines under it that belong to it, such as a stack trace |
| `y` | Copy the selected line, with the lines that belong to it |
| `h` `l`, `0` | Scroll sideways, reset |
| `w` | Wrap long lines, to read them in full |
| `/` | Filter the log (empty clears). Every word must match, ignoring case. `-word` leaves out lines with it, `/regex/` is a regular expression (Go syntax), `-/regex/` leaves out matches, and `"two words"` keeps a space. For example `network -heartbeat /id=\d+/`. A mistake in the expression is shown and the filter is not applied |
| `L` | Minimum level |
| `P` | Filters saved for this app (up to 10 per app and platform). Enter applies one, `x` forgets it, and the last row saves the filter and level the log has now |
| `p` | Pause or resume (nothing is lost) |
| `c` | Clear |
| `S` | Save what is shown to a file. A name ending in `.json` saves a JSON list with the fields of `logs --json` |
| `x` | Stop the stream |
| `J` | After `M` on an Android app: start the jank count again, so the heading shows the frames drawn from now on |
| `M` | Show the app's CPU and memory in the log's heading, sampled every 2 seconds: the share of one core it used since the last sample, and its resident memory. Again to hide them. It waits through a restart of the app. A simulator, an emulator or an Android phone; not an iPhone (use Instruments). On Android it also shows the share of janky frames (`jank 12%`) |
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

## Several logs, the mouse and the app list

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
| iPhone | Launch the app and show its output. Or the system log (needs `idevicesyslog`), optionally one app (bundle id), which must be running on the phone |

A crash is drawn in bold red, apart from other errors. Android crash reports, native signals and
React Native errors on Android, iOS uncaught exceptions and crash types, and React Native's
"Unhandled JS Exception" and "Invariant Violation" are recognised.

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

## Where you left off

When you quit, lazymobile remembers, for the folder it was started in, the selected device, the
open logs, each with its filter, level and wrap, and how they sit. The
next start in that folder puts them back, and opens the log again if its device is still running.
A log that launches an app comes back too, marked "not started", and `r` launches the app:
starting it on its own would launch the app again. The lines themselves are not kept.

Everything remembered, including the recent app ids and the saved filters, is in
`~/.local/state/lazymobile/state.json` (or `$XDG_STATE_HOME/lazymobile/state.json`), readable
only by you. Delete the file to forget it all.

## Colours

Set `NO_COLOR=1` and the dashboard draws no colours. Bold, dim and reverse video stay, so the
selection and the marked crash lines are still visible.
