"""Samples every process the running bridge owns, twice a second, into a CSV.

Usage: python3 benchmark/sample.py [seconds] [out.csv]   (defaults: 240, samples.csv)

The bridge must be running in the background (`baileys-cc-bridge install`). Send a message or two
in your chats while it samples, so the agents start and reply.
"""
import os
import subprocess
import sys
import time


def bridge_pid():
    out = subprocess.run(["launchctl", "print", f"gui/{os.getuid()}/baileys-cc-bridge"],
                         capture_output=True, text=True).stdout
    for line in out.splitlines():
        if line.strip().startswith("pid = "):
            return int(line.split("=")[1])
    sys.exit("baileys-cc-bridge isn't running in the background: run `baileys-cc-bridge install`")


def role(pid, ppid, name, root):
    if pid == root:
        return "bridge"
    if name == "node" and ppid == root:
        return "sidecar"
    if name.startswith("claude"):
        return "claude"
    if name.startswith("codex") or name == "node_repl":  # node_repl is one of Codex's helpers
        return "codex"
    return "tool:" + name  # a command an agent ran


def main():
    seconds = float(sys.argv[1]) if len(sys.argv) > 1 else 240
    out = open(sys.argv[2] if len(sys.argv) > 2 else "samples.csv", "w")
    root = bridge_pid()
    out.write("t,pid,ppid,role,rss_kb,cpu,comm\n")
    start = time.time()
    while time.time() - start < seconds:
        procs = {}
        for row in subprocess.run(["ps", "-A", "-o", "pid=,ppid=,rss=,pcpu=,comm="],
                                  capture_output=True, text=True).stdout.splitlines():
            parts = row.split(None, 4)
            if len(parts) == 5:
                procs[int(parts[0])] = (int(parts[1]), int(parts[2]), float(parts[3]), parts[4])
        if root not in procs:
            sys.exit("the bridge stopped")
        tree, frontier = {root}, [root]
        while frontier:
            parent = frontier.pop()
            for pid, (ppid, *_) in procs.items():
                if ppid == parent and pid not in tree:
                    tree.add(pid)
                    frontier.append(pid)
        t = round(time.time() - start, 1)
        for pid in tree:
            ppid, rss, cpu, comm = procs[pid]
            name = comm.rsplit("/", 1)[-1]
            out.write(f"{t},{pid},{ppid},{role(pid, ppid, name, root)},{rss},{cpu},{name}\n")
        out.flush()
        time.sleep(0.5)


if __name__ == "__main__":
    main()
