# Commands

```sh
lazymobile list [--json]
lazymobile boot "Pixel 9a" --cold --wait
lazymobile boot "Pixel 9a" --wipe --yes
lazymobile boot "iPhone 17" --no-window
lazymobile shutdown "Pixel 9a"
lazymobile shutdown "iPhone 17" --keep-simulator-app
lazymobile erase "iPhone 17" --yes
lazymobile delete "iPhone 17" --yes
lazymobile delete --unavailable [--yes]   # simulators Xcode can no longer run: lists them with their size, deletes with --yes
lazymobile uninstall "Pixel 9a" com.example.app --yes   # the app and its data
lazymobile install "Pixel 9a" ./app.apk   # an .app folder for a simulator or an iPhone
lazymobile install "Pixel 9a" app/build/outputs/apk --launch   # the newest build in a folder, then start it
lazymobile launch "Pixel 9a" com.example.app [--restart]
lazymobile stop "Pixel 9a" com.example.app   # keeps the app's data
lazymobile appearance "Pixel 9a" [dark|light]   # no mode shows the current one (Android 10 or newer)
lazymobile permission "Pixel 9a" grant camera com.example.app   # camera, location, photos, notifications...
lazymobile cleardata "Pixel 9a" com.example.app --yes   # emulators and simulators (a simulator keeps app group data and the keychain)
lazymobile location "Pixel 9a" 37.7749,-122.4194   # or: clear (simulators only)
lazymobile statusbar "iPhone 17" clean   # or: reset
lazymobile boot --all "iPhone*" "Pixel 9a" [--wait]         # several at once; a real phone is skipped
lazymobile install --all build/app.apk [--phones]           # on every running device that can take it
lazymobile boot --set matrix                                # a named set from .lazymobile.json
lazymobile action --list                                    # the commands in .lazymobile.json
lazymobile action "Pixel 9a" "Flutter run" --yes            # without --yes it only shows the command
lazymobile link "iPhone 17" "myapp://home?id=1&tab=2"   # a simulator may ask "Open in …?" for a deep link: tap Open in the Simulator window
lazymobile dev "Pixel 9a" reverse|menu|reload [--port 8081]   # Android only: reach Metro, open the dev menu, reload
lazymobile screenshot "Pixel 9a" [--out shot.png]   # never replaces a file
lazymobile crash "Pixel 9a" com.example.app [--save crash.txt]   # the app's latest crash report, on an iPhone too
lazymobile media "iPhone 17" photo.png clip.mp4   # photos and videos into a simulator's or emulator's gallery
lazymobile clipboard "iPhone 17" "text to paste"   # a simulator's clipboard; --get prints it, - reads standard input
lazymobile network "Pixel 9a" offline|online|gsm|edge|3g|lte|full   # an emulator with no connection, or a slow one
lazymobile create ios "Shop test" --type "iPhone 17" [--runtime 27.2]   # or: create android <name> [--image ..] [--profile pixel_9]; create --list shows the choices
lazymobile jank "Pixel 9a" com.example.app [--reset]   # janky frames and frame times, Android
lazymobile push "iPhone 17" com.example.app [--title T --body B | --payload push.json]   # simulators
lazymobile report "Pixel 9a" com.example.app [--out bug] [--since 5m] [--lines 200]   # a folder for a bug report
lazymobile changelog [0.4.0] [--all] [--json]   # what changed in this version, one version, or all
lazymobile record "Pixel 9a" [--out clip.mp4] [--duration 30s]   # Ctrl-C stops it and keeps the video
lazymobile logs "Pixel 9a" --app com.example.app --level warning
lazymobile logs "iPhone 17" --app com.example.app --mode console --json
lazymobile logs "Pixel 9a" --app com.example.app --since 5m --out errors.csv --filter crash   # .log .json .jsonl .csv .md
lazymobile logs "iPhone 17" --last-app   # the app id used most recently on iOS
lazymobile logs "Pixel 9a" --since 5m --tail 200   # read what was logged, print the last 200 lines, and end
lazymobile apps "iPhone 17" [--json]     # app ids used before, and the apps installed on it
lazymobile id "Pixel 9a"          # emulator-5554 when running
lazymobile builds [--json] [--limit 20] [--yes]   # --yes when .lazymobile.json sets the eas command
lazymobile doctor [--json]
lazymobile doctor --fix [--yes]     # shows each fix's command and asks before running it; --yes for scripts
lazymobile run [device] [-- tool flags]   # see run.md
lazymobile mcp                      # the MCP server, on stdio
lazymobile completion zsh|bash|fish
```

A device is an id, an adb serial, a name, or a unique part of a name. Commands that erase,
delete, uninstall or clear data need `--yes`. Exit codes: 0 success, 1 failure, 2 wrong usage.

App ids given to `logs` and `launch` are remembered, newest first, and shared with the dashboard.

Every command takes `--json`, apart from `run`, `mcp`, `completion` and `version`. A failure is then printed on stdout as
`{"error": {"code", "message", "hint"}}`, with the same exit code, so a script can tell `not_found`
from `tool_missing`. `logs --json` prints one object per line (`text`, `level` from 1 verbose to 6
fatal, `pid`, and `crash` on the lines of a crash). Every shape is in [docs/json.md](docs/json.md).
