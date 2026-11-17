# Benchmark

What baileys-cc-bridge and its agents use on a Mac. Measured on an Apple M6 with 16 GB of memory
(macOS 27.2.1): one Claude chat and one Codex chat each answered a one-line question while
`sample.py` recorded every process the bridge runs, twice a second, for 4 minutes.

## Memory and CPU

| Process | Runs | Memory (RSS) | Activity Monitor | CPU |
|---|---|---|---|---|
| `baileys-cc-bridge`: the daemon, panel and tools | always | 16–20 MB | 11 MB | about 0% |
| Node sidecar: the WhatsApp connection | always | 70–85 MB | 77 MB | about 0%, up to 3% per message |
| Claude Code, one per chat | from the chat's first message until the bridge restarts | 295 MB while replying, 260–375 MB idle | 160–170 MB | up to half a core while replying |
| Codex, one per chat, with its helpers | from the chat's first message until the bridge restarts | 265 MB while replying, 235 MB idle | 120 MB | up to 40% of a core while replying |

- RSS counts memory shared with other processes. Activity Monitor's figure (the footprint) is closer
  to what each process really costs.
- The models run in the cloud, so between messages the CPU stays near zero. It rises while an agent
  starts or runs commands; those commands (a shell, `curl`) come and go at a few MB each.
- Each chat keeps its agent open after its first message, so memory grows with the number of chats
  that have talked since the bridge started.
- Claude's RSS kept rising for a few minutes after its reply, to 375 MB, then held. Its footprint
  stayed at 160 MB.

## Disk

| What | Size |
|---|---|
| The binary, for Apple Silicon and Intel in one file | 20 MB |
| The sidecar's npm packages, installed on first start | 48 MB |
| The linked device's keys | 22 MB |
| Settings, chat folders and the log | under 1 MB, plus the attachments people send |

## Running it

With the bridge running in the background:

```sh
python3 benchmark/sample.py 240 samples.csv   # every process the bridge runs, for 4 minutes
python3 benchmark/summarize.py samples.csv
```

Send a message in a chat or two while it samples. `footprint -p <pid>` gives the Activity Monitor
figure for one process.
