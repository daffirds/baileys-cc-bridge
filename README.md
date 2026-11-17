# baileys-cc-bridge

Bridges whitelisted WhatsApp chats to Claude Code or Codex sessions on your Mac. Every message in a
whitelisted chat goes to that chat's own agent session, even mid-turn. The agent answers with tools:
send messages, files and stickers, react, and set reminders.

```
WhatsApp ── Baileys (node sidecar) ── bridge (Go) ── claude -p stream-json | codex app-server
                                         └─ MCP on 127.0.0.1 ← whatsapp tools
```

- **One chat, one session.** The CLI process starts on the chat's first message and stays up. After
  a restart, the next message resumes the same conversation (`--resume`, `thread/resume`).
- **The bridge never speaks.** Everything people see is a tool call the agent made. Its plain text
  goes nowhere.
- **Off means missed.** Messages that arrive while the bridge is off are not replayed.
- It links as a device of your account, so the agent writes as you. Your own messages in a
  whitelisted chat (typed on the phone) reach the agent too.

## Setup

Needs macOS, Go 1.26+, Node 22.18+, and `claude` and/or `codex` installed and logged in.

```sh
go build -o ~/.local/bin/baileys-cc-bridge ./cmd/baileys-cc-bridge
baileys-cc-bridge install   # from now on it runs in the background, and opens its panel
```

The bridge then runs in the background with no terminal: it starts when you log in and restarts if it
crashes. Its panel is on 127.0.0.1:9999 (bookmark the link, or open it with `baileys-cc-bridge ui`):
scan its QR code (WhatsApp → Settings → Linked devices), then pick chats, engines and folders.
Logging out there unlinks the number and shows a new QR code, so another number can take over.

`uninstall` stops it; `run` runs it in a terminal instead (Ctrl-C stops). The first connection
installs the sidecar's npm packages under `~/.baileys-cc-bridge/sidecar`.

## Config

`~/.baileys-cc-bridge/<number>/config.yaml`, edited in the panel or by hand (see [config.example.yaml](config.example.yaml)).
A running bridge applies changes within seconds. A chat whose settings changed gets them with its next message, once its agent is idle, so an agent is never cut off mid-task.

| Key | Default | |
|---|---|---|
| `engine` | `claude` | `claude` or `codex` |
| `autonomy` | `auto` | `auto`: the engine's own reviewer · `full`: no prompts, no sandbox · `readonly` |
| `model`, `effort` | CLI default | |
| `workdir` | `~/.baileys-cc-bridge/<number>/chats/<chat>` | where the agent runs; must exist |
| `system_prompt` | one for groups, one for direct chats | what the agent is told after the bridge's own fixed instructions (how it reaches WhatsApp) |
| `footer` | `true` | puts _Assisted by Claude_ (or _Codex_) under every message and caption the agent sends |

Each entry under `chats` has a `jid`, a `name` and any of these. A `jid` is a group (`…@g.us`), a person (`…@s.whatsapp.net`, or just a phone number with country
code) or yourself ("Message yourself"). `chats` prints yours.

## Tools

The agent gets an MCP server named `whatsapp`, bound to its chat:

| Tool | |
|---|---|
| `send_message` | text; `reply_to` quotes a message, `mentions` @-mentions people by number, `mention_all` everyone in the group |
| `send_file` | photo, video or audio inline; anything else as a document; `voice` for OGG voice notes |
| `send_sticker` | any image, converted to a 512×512 WebP |
| `react` | emoji on a message (default: the latest one); empty removes it |
| `chat_info` | a group's name, description and members (names, numbers, admins), or who a direct chat is with |
| `set_reminder` · `list_reminders` · `cancel_reminder` | a note the agent gets back in its chat at a set time (`in` 10m, or `at` a local time), as a `<reminder>` block; one due while the bridge was off arrives when it is back |

`chat` can point a tool at another whitelisted chat, never at one outside the list. Incoming
attachments are saved to `.baileys-cc-bridge/media/` in the agent's folder and the path is given to
the agent.

## Commands

| | |
|---|---|
| `run [-debug] [-addr] [-open]` | bridge the configured chats and serve the panel |
| `ui` | open the running bridge's panel |
| `install` · `uninstall` | run in the background (a launchd agent: starts at login, restarts after a crash), or stop |
| `login [-phone N]` · `logout` | link or unlink this Mac from the terminal |
| `chats [filter]` | your JID and your groups' JIDs |

Only one of `run`, `chats`, `login` and `logout` can be connected to WhatsApp at a time: a second
connection would make WhatsApp drop the first.

## Files

`~/.baileys-cc-bridge/` holds `auth/` (the linked device's WhatsApp credentials), `sidecar/`,
`bridge.log` (the background bridge's log),
`panel.token` (the panel link's token, kept so a bookmark keeps working) and one folder per number that has been linked, so another number never shares a chat's conversation:

```
~/.baileys-cc-bridge/<number>/
  config.yaml     the whitelist
  state.json      each chat's conversation id
  reminders.json  reminders not yet due
  names.json      names learned from senders, for prompts and chat_info
  chats/<chat>/   each chat's default folder
```

The bridge's own files for a chat live in `.baileys-cc-bridge/` inside the agent's folder, so a
working directory that is a real project gets one hidden folder and nothing else. It ignores itself
in git. The conversations themselves are the CLIs' own (`~/.claude/projects`, `~/.codex/sessions`).

## Security

Anyone in a whitelisted group can steer an agent that has a shell on your Mac. Keep groups on
`auto` or `readonly`, and use `full` only where you trust everyone. The tool endpoint and the
panel listen on loopback only, behind random tokens.

WhatsApp does not allow unofficial clients. Bans mostly hit bulk or unsolicited messaging, but use a
spare number if yours matters.

## Development

- `web/`: the panel (Vite + React). `npm run build` writes `internal/webui/dist`, which is committed
  and embedded.
- `sidecar/`: the Baileys side. `npm run check` type-checks it; the binary embeds it.
