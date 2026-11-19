# Changelog

What changed in each version. The program has these notes built in: `lazymobile changelog` prints
them, and the dashboard shows what is new once after an upgrade.

## Unreleased

### Run and report
- `lazymobile run`: pick a device, start it if it is off, and run this folder's app on it with the
  tool you already use (`flutter run`, the project's own `expo run:ios` or `run:android`, or the
  action named `run` in `.lazymobile.json`). Its keys, such as hot reload, keep working. It asks
  which way to run (run dev, run prod) and which device when it cannot tell, and remembers the
  answers for the folder (#42)
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
- `lazymobile crash <device> <app-id>` and "Show its latest crash report" in the app menu (#37)

### For agents
- `lazymobile mcp` serves the device commands to a coding agent over MCP (stdio). Nothing that
  erases data is served (#43)

### Other
- `lazymobile changelog` prints these notes, and the dashboard shows what is new once after an
  upgrade (#41)
- The dashboard says in one line when a newer version is out. It looks at most once a day, with a
  plain request to GitHub's public release list that carries nothing about you. Set
  `LAZYMOBILE_NO_UPDATE_CHECK=1` to turn it off (#41)
- A simulator's log no longer shows the entries launchd and `log` write about reading it (#44)
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
