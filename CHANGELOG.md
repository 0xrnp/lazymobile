# Changelog

What changed in each version. The program has these notes built in: `lazymobile changelog` prints
them, and the dashboard shows what is new once after an upgrade.

## Unreleased

### Run and report
- `lazymobile run`: pick a device, start it if it is off, and run this folder's app on it with the
  tool you already use. A native Kotlin or Swift app is built with `./gradlew` or `xcodebuild`,
  then installed and launched (Xcode builds on simulators). Flutter, React Native and Expo run
  with `flutter run`, `react-native run-ios` and `run-android`, or the project's own `expo run`.
  The actions named `run` in `.lazymobile.json` come first, then your `package.json` scripts that
  run the app (such as `ios` with `expo run:ios`). Keys such as hot reload keep working. It asks which way to run and which device when it cannot tell, offers only
  the ways that fit a device you name, and remembers the answers for the folder (#42)
- `lazymobile run --here` runs your tool inside lazymobile: the tool on the left, the app's log on
  the right, and `ctrl+]` to switch to the full dashboard and back while the tool keeps running.
  Drag the line between the panes, or press `shift+←` and `shift+→` from either pane, to resize
  them (these two keys are lazymobile's, so a tool cannot receive them). `--no-log`, or `ctrl+p`
  then `l`, hides the log so the tool has the whole width (#42)
- `ctrl+p` opens a quick menu on the dashboard and in the run view: `s` saves a screenshot and copies
  it as a picture to paste into a chat, `r` starts and stops a screen recording and copies the video
  as a file, `c` copies the newest 200 lines of the open log, as its filter shows them, as text to
  ask an AI about, and `b`, `o`, `t`
  and `m` are the bug report, app menu, link and dark mode. A recording shows `● REC`, and quitting
  asks and saves it first (#42)
- A dashboard open in the same folder follows a run: it selects the device and opens the app's
  log. `lazymobile run --split` opens that dashboard in a new pane in Rex, tmux or Zellij (#42)
- `lazymobile report <device> <app-id>`, and "Save a bug report" in the dashboard's app menu: a new
  folder with a screenshot, the app's last log lines, its latest crash report, and the device and
  app version (#39)
- `lazymobile crash <device> <app-id>` and "Show its latest crash report" in the app menu (#37), on
  a connected iPhone too: the report is copied from the phone with Xcode's `devicectl`, and never
  removed from it (#38)

### For agents
- `lazymobile mcp` serves the device commands to a coding agent over MCP (stdio). Nothing that
  erases data is served (#43)
- The MCP server sends screenshots scaled to at most 1568 pixels as JPEG, about 20 times smaller
  than the PNG (`full_size` for the PNG), and serves `crash_report` and `set_status_bar`. It tells
  the agent how to start: list the devices, prefer a simulator, and read one app's log
- `docs/coding-agents.md`: setting up Claude Code, Cursor, Codex and VS Code, what to ask an
  agent, and a skill file (`skills/lazymobile/SKILL.md`) for agents that use the commands with
  `--json`

### Other
- `lazymobile install <device> <folder> --launch` installs the newest build in a folder, such as
  Gradle's `app/build/outputs/apk`, and starts it
- `M` in a log shows the app's CPU and memory in the heading, every 2 seconds, on a simulator,
  an emulator or an Android phone (#40)
- An iPhone takes dark and light mode, a simulated location, a tidy status bar and a link through
  Xcode's `devicectl`, as `appearance`, `location`, `statusbar` and `link` already do on a simulator.
  Each change prints the command that undoes it. Run on an iPhone 17 Pro with iOS 27.0, everything worked except the status bar, which that iOS refuses, and lazymobile says so. A link on an iPhone goes to Safari when it is a web
  address, and to the app named with `link --app` otherwise. Agents refuse a real phone unless the call
  says `physical` true. Face ID is not offered
- A location that starts with a minus sign, such as `-33.8688,151.2093`, was read as a flag. It is a
  place now
- Logs can be exported in the layout you need. `S` in the dashboard asks which lines (what is shown,
  everything received, errors and crashes only, or from the selected line) and picks the layout from the
  file name: `.log`, `.json`, `.jsonl`, `.csv` or `.md` for pasting into an issue. `lazymobile logs <device>
  --out <file>` does the same from the command line, with `--format` and `--filter`, and records until
  ctrl+c when no `--tail` or `--since` is given. Agents have `export_logs`. A saved file is never replaced,
  which the dashboard's old save did silently, and only you can read it
- Agents can work in an Android emulator's app: `ui_tree` reads the screen as text, with where to tap
  each label, text and id, and the screen size. `tap` takes one of those names (or x and y) and refuses,
  naming the candidates, when it matches nothing or several. `type_text`, `swipe` and `press_key` fill
  a field, scroll and press back, home or enter. A password field is never read out. An iOS simulator
  has no such Apple command, and a phone is left alone. Also new for agents: `app_usage` (CPU and
  memory) and `record_screen`. A `screenshot` now says the screen's size, since the picture may be
  scaled down
- Agents can run the project: `run_start`, `run_status`, `run_reload` and `run_stop` (in `lazymobile mcp`)
  start the tool the project already uses (`flutter run`, `expo run`, a package.json script, or the
  `run` action in `.lazymobile.json`) on a device, which is started first when it is off. The tool
  runs on a terminal of its own, so `run_reload` sends it `r` (or `R`) like a keypress, and each
  answer carries the last lines of its output and, once it ends, its exit code. At most three runs
  go at once, they end with the server, and a command from `.lazymobile.json` needs `yes`, as `--yes`
  does. Run on a real Flutter app: started, hot reloaded and stopped on an emulator
- On Android, the app picker and `lazymobile apps` no longer call Android's own apps (Settings, Clock)
  "not in the installed list" when you have used them before. An app that is on neither list still is
- `lazymobile media <device> <file>...` and `add_media` for agents (`A` in the dashboard) put photos and
  videos into a simulator's or an emulator's gallery, so an app that picks a photo has one to pick. A
  simulator also takes contacts (`.vcf`). On an emulator the file is copied into Pictures or Movies
  and never replaces one that is there: a second `test.png` becomes `test-2.png`. A real phone is left alone
- `lazymobile clipboard <device> <text>` (or `-` to read the text from standard input, `--get` to print
  it), `set_clipboard` for agents and `V` in the dashboard put text on an iOS simulator's clipboard, ready
  to paste into a field. An Android emulator has no command for it and says so
- Screenshots of a real iPhone: `lazymobile screenshot`, `p` in the dashboard and the `screenshot` agent tool now
  work on an iPhone through Xcode's `devicectl`, while it is unlocked. Screen recording is built the same way, but
  an iPhone whose iOS does not offer it to Xcode (an iPhone 17 Pro on iOS 27.0 did not) gets a clear
  message and the QuickTime Player way instead
- `lazymobile cleardata` and "Clear its data…" in the app menu now work on an iOS simulator, not only
  on an emulator: the app is stopped and its data folder emptied (Documents, preferences, caches),
  so it starts as if just installed. Its app group folders and keychain are kept, and a real
  iPhone is left alone
- `lazymobile link` and `open_link` tell you when an iOS simulator may be waiting for a tap: Xcode
  27's simulator asks "Open in …?" before it opens a deep link, and only a tap in the Simulator
  window answers it. Web addresses open at once
- `NO_COLOR=1` turns the dashboard's colours off (bold, dim and reverse stay). It already did; it is
  now written down in `docs/dashboard.md`
- `lazymobile network <device> offline|online|gsm|edge|3g|lte|full` and `set_network` for agents:
  an Android emulator with no connection (airplane mode), or held to a slow speed and delay, to
  see how an app copes (`N` in the dashboard). `online` puts it back. Emulators only: a phone is left alone, and a
  simulator uses the Mac's network
- `lazymobile create ios <name> --type "iPhone 17"` and `lazymobile create android <name>` make a
  simulator or an emulator from the command line, like `n` in the dashboard. The newest runtime or
  system image is used unless you name one (`--runtime 18`, `--image android-35`, `--profile
  pixel_9`), a name that fits more than one choice lists them, and `create --list` shows what is
  installed
- An emulator made from lazymobile no longer goes missing when `XDG_CONFIG_HOME` is set: Android's
  `avdmanager` then wrote it where the emulator does not look. This also affected `n`
- `lazymobile jank <device> <app-id>` and `app_jank` for agents: how many frames an Android app
  drew late and how long frames took (50th to 99th percentile), from `dumpsys gfxinfo`. `--reset`
  starts the count again, so you can read it, use the app, and read it again. Emulators and
  phones; iOS is refused and points to Instruments. In the dashboard, `M` adds `jank 12%` to the
  heading of an Android app's log, and `J` starts the count again
- `lazymobile push <device> <app-id>`, "Send a test push notification" in the app menu, and
  `send_push` for agents: a notification with a title and text, or your own payload file, to an
  app on an iOS simulator (#34)
- Tab completion for zsh, bash and fish: commands, flags, device names, the app ids you used and
  files. Set it up with `lazymobile completion zsh`, `bash` or `fish` (#36)
- `lazymobile changelog` prints these notes, and the dashboard shows what is new once after an
  upgrade (#41)
- The dashboard says in one line when a newer version is out. It looks at most once a day, with a
  plain request to GitHub's public release list that carries nothing about you. Set
  `LAZYMOBILE_NO_UPDATE_CHECK=1` to turn it off (#41)
- A simulator's log no longer shows the entries launchd and `log` write about reading it (#44)
- A Swift fatal error (a forced unwrap of nil, an index out of range, `fatalError()`) is marked as
  a crash in an iOS log, so `e` jumps to it and `crashes_only` keeps it
- Building from source needs Go 1.27.4 or newer

## 0.4.0 - 2026-11-17

### Apps
- Launch, restart and stop an app on a running device (#9)
- Install an app from the devices pane, and on a connected iPhone (#12)
- Boot and install on several devices at once (#20)
- Run your project's own commands from `.lazymobile.json` on a device (#17)

### Device actions
- Open a deep link and take a screenshot (#11)
- Record the screen of a simulator, emulator or Android phone
- Switch between dark and light mode
- Set the location and tidy the status bar
- Grant or revoke an app's permission and clear its data
- The disk each simulator's or emulator's data uses, in a column of the devices list, in
  `lazymobile list` and in its JSON. Simulators Xcode can no longer run (an update removed their runtime) are counted, and
  `U` in the dashboard or `lazymobile delete --unavailable` deletes them, after listing them with
  their sizes and asking

### Logs
- Mark crashes and jump between errors with `e` and `E` (#13)
- Filters: excluded words, regular expressions, presets saved per app, and JSON export (#19)
- Read a log and stop with `logs --tail N` and `--since 5m` (#33)
- Show only one app's log on a connected iPhone (#32)

### React Native
- `lazymobile dev`, or `D` in the dashboard: reach Metro on port 8081, open the developer menu, or
  reload the app on Android (#18)

### Other
- `doctor --fix`, with checks for idevicesyslog and the Expo login (#21)
- `--json` on every command, and failures printed as JSON (#10)

### Changes in behaviour
- Log filter words must all match. `-word` leaves a word out and `/regex/` is a regular expression.
- An `eas` command from `.lazymobile.json` is shown before it runs, and `builds` needs `--yes`.

## 0.3.0 - 2026-11-15

- Show the installed version of an app in the app list and the log heading (#2)
- Uninstall an app from a device, from the dashboard or the command line (#4)
- Show any number of logs side by side, stacked or one at a time (#5)
- Show the keys as a menu you can filter and run (#6)
- Filter the app picker and show which used apps are on the device (#7)

## 0.2.3 - 2026-11-14

- Remember app ids and the last view, offer installed apps, and click to select

## 0.2.2 - 2026-11-14

- Read whole log lines, scroll with the mouse, and run inside Neovim

## 0.2.1 - 2026-11-14

- Follow device changes live, quit the simulator app, and resize panes

## 0.1.0 - 2026-11-14

- The first version: a terminal dashboard for iOS simulators, Android emulators and phones
