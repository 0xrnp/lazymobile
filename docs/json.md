# JSON output

Every command takes `--json`. It is meant for scripts and coding agents: what a command prints is
one JSON object, or for `logs`, one object per line. The exit codes are the same with and without
`--json`: 0 success, 1 failure, 2 wrong usage.

## Failures

With `--json`, a failure is printed on **stdout**, and nothing is printed on stderr:

```json
{
  "error": {
    "code": "not_found",
    "message": "No device matches \"Galaxy\"",
    "hint": "Run `lazymobile list` to see the devices."
  }
}
```

`code` is for a program to branch on. `message` and `hint` are for a person, and their wording can
change. `hint` says what to do next, when there is something to say. A failed tool also gives
`argv` (what was run), `exit_code` and `stderr`.

| `code` | Meaning |
| --- | --- |
| `usage` | The command was typed wrongly: a missing argument, an unknown flag, or a destructive command without `--yes`. Exit code 2 |
| `not_found` | No such device, app or Expo project |
| `invalid_state` | The device is in the wrong state, such as not running, or the name fits several devices |
| `unsupported` | The device cannot do this, such as booting a real phone |
| `tool_missing` | `xcrun`, `adb`, `emulator` or `eas` was not found. `hint` says how to install it |
| `timeout` | A tool did not answer in time |
| `command_failed` | A tool ran and failed |
| `parse_failed` | A tool's output could not be read |

A command interrupted with Ctrl-C exits with 130 and prints nothing.

## Commands that change a device

`boot`, `shutdown`, `erase`, `delete`, `install`, `uninstall`, `launch`, `stop`, `link`, `screenshot`, `appearance` (with a mode), `location`, `statusbar`, `permission`, `cleardata`, `record` and `action` print:

```json
{
  "ok": true,
  "action": "launch",
  "device": { "id": "...", "name": "Pixel 9a", "platform": "android", "kind": "emulator", "state": "booted" },
  "app": "com.example.app"
}
```

- `action` is the command, except that `launch --restart` is `restart`.
- `device` is the device as it was found, so its `state` is from before the action.
- `app` is set for `uninstall`, `launch`, `restart` and `stop`. `url` is set for `link`. `path` is set for `install` (the file given) and `screenshot` (the full path of the PNG that was saved).
- `warnings` is a list of sentences, set when the action worked but left something behind, such as
  Simulator staying open after `shutdown`.

`appearance` also has `appearance`, the mode it switched to (`dark` or `light`). `location` has
`location`, which is `latitude,longitude` or `cleared`. `statusbar` has `action` set to
`statusbar_clean` or `statusbar_reset`. `permission` has `action` set to `permission_grant` or
`permission_revoke`, and `permission`, the name that was given. `action` has `action` set to
`action`, `name`, the project action that ran, and `command`, its argument list with the device
filled in. The command's own output goes to stderr with `--json`, so stdout stays one object.

`action --list --json` prints a list of `{"name", "command", "platform", "dir"}`, where `command`
still has its placeholders. An empty list is `[]`.

`id` prints `{"device": {...}, "id": "emulator-5554"}`. `appearance` without a mode prints
`{"device": {...}, "appearance": "dark"}`, where Android can also say `auto` for a device that
follows the time of day.

## Commands that read

| Command | Prints |
| --- | --- |
| `list` | `{"devices": [...], "errors": [...]}`. A tool that failed is an entry in `errors`, shaped like the failure object above, and the devices that could be listed are still there. Exit code 0 unless nothing could be listed |
| `apps <device>` | `{"device": "name", "apps": [{"id", "name", "version", "build", "recent", "installed"}]}` |
| `builds` | `{"project": "name", "builds": [...]}`, the Expo (EAS) builds of the project in this folder |
| `doctor` | `{"os", "found": [{"name", "detail"}], "problems": [{"code", "platform", "severity", "message", "hint"}]}`. `severity` is `error`, `warning` or `info`. Exit code 1 when a problem has severity `error`, and then no `error` object is printed: the report is the answer |
| `logs <device>` | One object per line: `{"text", "level", "pid", "crash"}`. `level` goes from 1 (verbose) to 6 (fatal) and is left out for a line that has none, which belongs to the line before. `crash` is `true` on the lines of a crash, such as an Android `FATAL EXCEPTION` report, and left out otherwise. A failure before or after the lines is a last line with an `error` object. The dashboard's `S` key saves the shown lines in the same fields, as one JSON list, when the file name ends in `.json` |

A device in `device` or `devices` has `id`, `name`, `platform` (`ios` or `android`), `kind`
(`simulator`, `emulator` or `physical`), `state` (`booted`, `shutdown`, `connected`,
`disconnected`, `offline`, `unauthorized`), and when known `os_version`, `model`, `transport`
(`usb`, `wifi` or `local`) and `serial` (the id `adb -s` takes).

New fields can be added to any object. A field that is there will not change its meaning without
a new version number noted in the release notes.
