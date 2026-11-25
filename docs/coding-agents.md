# lazymobile for coding agents

A coding agent can change your app's code, but it cannot see whether the change works on a phone.
lazymobile gives it the device side: find and boot a simulator or emulator, install and open the
app, take a screenshot, read the app's log and its latest crash report. It goes through the same
code as the dashboard, so you can watch in the dashboard what the agent does.

There are two ways to connect an agent:

- **The MCP server**, `lazymobile mcp`. The agent sees a list of tools. Use this with Claude Code,
  Cursor, Codex, VS Code and any other client that runs MCP servers.
- **The commands with `--json`.** Any agent that can run a shell command can use them, with no
  setup. A [skill file](#without-mcp-the-skill) teaches the agent which commands to use.

## Set up the MCP server

Install lazymobile first (see the [README](../README.md#install)), and check that `lazymobile
doctor` is happy. Then add the server to your agent.

### Claude Code

```sh
claude mcp add lazymobile -- lazymobile mcp
```

Add `--scope user` to have it in every project, not only this one.

### Cursor

Add this to `.cursor/mcp.json` in your project, or to `~/.cursor/mcp.json` for every project:

```json
{
  "mcpServers": {
    "lazymobile": { "command": "lazymobile", "args": ["mcp"] }
  }
}
```

Then check in Cursor's settings that lazymobile is listed and enabled among the MCP servers.

### Codex

```sh
codex mcp add lazymobile -- lazymobile mcp
```

Or in `~/.codex/config.toml`:

```toml
[mcp_servers.lazymobile]
command = "lazymobile"
args = ["mcp"]
```

### VS Code (Copilot)

In `.vscode/mcp.json`:

```json
{
  "servers": {
    "lazymobile": { "type": "stdio", "command": "lazymobile", "args": ["mcp"] }
  }
}
```

### Any other client

The server talks over stdio. The command is `lazymobile` and its one argument is `mcp`.

### When the agent cannot start it or finds no devices

An editor opened from the Dock does not get the `PATH` of your shell, so it may not find
`lazymobile`, or lazymobile may not find the Android SDK.

- Use the full path as the command. `command -v lazymobile` prints it, for example
  `/opt/homebrew/bin/lazymobile`.
- lazymobile looks for the Android SDK in `ANDROID_HOME`, `ANDROID_SDK_ROOT` and the place Android
  Studio installs it. If yours is somewhere else, pass `ANDROID_HOME` to the server. Most clients
  take an `env` object next to `command`:

  ```json
  "lazymobile": {
    "command": "/opt/homebrew/bin/lazymobile",
    "args": ["mcp"],
    "env": { "ANDROID_HOME": "/path/to/Android/sdk" }
  }
  ```

- Ask the agent to call the `doctor` tool. It says what is missing.

## The tools

| Tool | What it does |
| --- | --- |
| `list_devices` | Every simulator, emulator and phone, with its state and id |
| `list_apps` | The app ids used before, and the apps installed on a running device |
| `doctor` | What is installed, what is missing, and how to fix it |
| `boot`, `shutdown` | Start a simulator or emulator and wait until it is ready, or stop it |
| `install` | Install an `.apk`, or an `.app` folder, on a running device |
| `launch`, `stop` | Open an app (optionally restarting it), or stop it. Its data is kept |
| `open_link` | Open a web address or a deep link, such as `myapp://settings`. On an iOS simulator a deep link may stop at an "Open in …?" question that only a tap answers, and nothing here can tap, so an agent should expect that or use a web address |
| `screenshot` | The screen as an image, scaled to at most 1568 pixels on its longest side (`full_size` for the PNG as taken) |
| `read_logs` | The last lines already logged, filtered by app, level, text, time or crashes. It returns and does not follow the log |
| `crash_report` | The latest crash report of an app |
| `set_appearance` | Dark or light mode |
| `set_location` | Put a simulator or emulator at a latitude and longitude |
| `set_permission` | Grant or revoke a permission, such as camera or photos |
| `set_status_bar` | A tidy status bar for screenshots, or the normal one back |
| `set_network` | Make an Android emulator offline, online or slow (gsm, edge, 3g, lte), then back to online |
| `app_jank` | How smoothly a running Android app draws: janky frames and frame times, with an optional reset |
| `send_push` | A test push notification to an app on an iOS simulator, with a title and text or a whole payload |

The server also tells the agent how to start: list the devices, pass an id, prefer a simulator or
an emulator, and pass the app to `read_logs`.

### What an agent cannot do

- **Erase anything.** Erasing a device, deleting one, uninstalling an app and clearing its data are
  not served. They stay on the command line, behind `--yes`, for you.
- **Tap or type.** The agent can see the screen but not press a button. Deep links, permissions and
  locations get it to many screens. To drive the interface, add a tool built for it next to
  lazymobile, such as [Maestro's MCP server](https://docs.maestro.dev/get-started/maestro-mcp) or
  [mobile-mcp](https://github.com/mobile-next/mobile-mcp).
  [#45](https://github.com/0xrnp/lazymobile/issues/45) is about doing this in lazymobile.
- **Build your app.** Build with your usual tool (the agent can run it), then `install` and
  `launch`. Running `lazymobile run` from an agent is not served yet.

### Safety

- The server asks the agent to prefer simulators and emulators, but a connected phone is in the
  device list and the agent can install and launch on it. Disconnect a phone you do not want
  touched.
- Log lines and crash reports are written by apps and by the device. An app, or a web page it
  shows, could write text meant to steer the agent. The server tells the agent to treat them as
  data, but read what it does after reading a log as you would any agent action.
- A screenshot shows whatever is on the screen, including anything private in the app.

## Things to ask

These work as written once the server is added. Replace the app id and the path with yours.

**Check a change on a simulator**

> Build the app for the iOS simulator, install it on a booted iPhone simulator with lazymobile,
> launch com.example.app, and take a screenshot. Then read its log for errors since the launch.

**Find out why it crashes**

> Launch com.example.app on the Android emulator. If it closes, get the crash report with
> lazymobile and tell me which line of our code it points to.

**Screenshots for a pull request**

> On the booted simulator, tidy the status bar, then open myapp://settings and take a screenshot in
> light mode and in dark mode. Put the status bar back afterwards.

## Without MCP: the skill

The device commands take `--json` and print one JSON object (`logs` prints one per line), so an
agent can use lazymobile through its shell with no server. [docs/json.md](json.md) describes the
output and the error codes.

[`skills/lazymobile/SKILL.md`](../skills/lazymobile/SKILL.md) is a skill file that tells an agent
which commands to use and how. It follows the Agent Skills format, which Claude Code, Codex and
Cursor read. Copy its folder to where your agent looks for skills: for Claude Code,
`~/.claude/skills/` or `.claude/skills/` in the project; for Cursor and Codex, `.agents/skills/` in
the project. Their own docs name the folder in your home directory, which has changed between
versions. For Claude Code:

```sh
mkdir -p ~/.claude/skills/lazymobile
curl -fsSL https://raw.githubusercontent.com/0xrnp/lazymobile/main/skills/lazymobile/SKILL.md \
  -o ~/.claude/skills/lazymobile/SKILL.md
```

The skill costs the agent's context only when a task needs it. The MCP server's tool list is in
every conversation, but needs no shell access. Either works. Use the one your agent handles better.
