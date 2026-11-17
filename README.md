# bailey-cc-bridge

Bridges whitelisted WhatsApp chats to Claude Code or Codex sessions on your Mac. Every message in a
whitelisted chat goes to that chat's own agent session, even mid-turn. The agent answers with tools:
send messages, files and stickers, and react.

```
WhatsApp ── Baileys (node sidecar) ── bridge (Go) ── claude -p stream-json | codex app-server
                                         └─ MCP on 127.0.0.1 ← whatsapp tools
```

- **One chat, one session.** The CLI process starts on the chat's first message, stops after
  `idle_timeout`, and resumes the same conversation (`--resume`, `thread/resume`) on the next one.
- **The bridge never speaks.** Everything people see is a tool call the agent made. Its plain text
  goes nowhere.
- **Off means missed.** Messages that arrive while the bridge is off are not replayed.
- It links as a device of your account, so the agent writes as you. Your own messages in a
  whitelisted chat (typed on the phone) reach the agent too.

## Setup

Needs macOS, Go 1.26+, Node 22.18+, and `claude` and/or `codex` installed and logged in.

```sh
go build -o ~/.local/bin/bailey-cc-bridge ./cmd/bailey-cc-bridge
bailey-cc-bridge login   # scan the QR: WhatsApp → Settings → Linked devices (or -phone for a code)
bailey-cc-bridge ui      # pick chats, engines and working directories in the browser
bailey-cc-bridge run     # foreground; Ctrl-C stops
```

The first command that connects installs the sidecar's npm packages under `~/.bailey-cc-bridge/sidecar`.

## Config

`~/.bailey-cc-bridge/config.yaml`, edited with `ui` or by hand (see [config.example.yaml](config.example.yaml)).
A running bridge applies changes within seconds.

| Key | Default | |
|---|---|---|
| `engine` | `claude` | `claude` or `codex` |
| `autonomy` | `auto` | `auto`: the engine's own reviewer · `full`: no prompts, no sandbox · `readonly` |
| `model`, `effort` | CLI default | |
| `workdir` | `~/.bailey-cc-bridge/chats/<chat>` | where the agent runs; must exist |
| `prompt` | | appended to the bridge's instructions |
| `idle_timeout` | `30m` | |

`defaults` sets them for every chat; each entry under `chats` (`jid`, `name`) can override any of them.
A `jid` is a group (`…@g.us`), a person (`…@s.whatsapp.net`, or just a phone number with country
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

`chat` can point a tool at another whitelisted chat, never at one outside the list. Incoming
attachments are saved to `.bailey-cc-bridge/media/` in the agent's folder and the path is given to
the agent.

## Commands

| | |
|---|---|
| `run [-debug]` | bridge the configured chats |
| `ui [-addr] [-no-open]` | config UI on 127.0.0.1 |
| `login [-phone N]` · `logout` | link or unlink this Mac |
| `chats [filter]` | your JID and your groups' JIDs |

Only one of `run`, `chats`, `login`, `logout` and the UI's group refresh can be connected to
WhatsApp at a time: a second connection would make WhatsApp drop the first.

## Files

`~/.bailey-cc-bridge/`: `config.yaml`, `auth/` (WhatsApp credentials), `state.json` (each chat's
conversation id), `groups.json`, `names.json` (names learned from senders, for prompts and
`chat_info`), `sidecar/`, and `chats/<chat>/`, the default folder of each chat's agent.

The bridge's own files for a chat live in `.bailey-cc-bridge/` inside the agent's folder, so a
working directory that is a real project gets one hidden folder and nothing else. It ignores itself
in git. The conversations themselves are the CLIs' own (`~/.claude/projects`, `~/.codex/sessions`).

## Security

Anyone in a whitelisted group can steer an agent that has a shell on your Mac. Keep groups on
`auto` or `readonly`, and use `full` only where you trust everyone. The tool endpoint and the
config UI listen on loopback only, behind random tokens.

WhatsApp does not allow unofficial clients. Bans mostly hit bulk or unsolicited messaging, but use a
spare number if yours matters.

## Development

- `web/`: the config UI (Vite + React). `npm run build` writes `internal/webui/dist`, which is committed
  and embedded.
- `sidecar/`: the Baileys side. `npm run check` type-checks it; the binary embeds it.
