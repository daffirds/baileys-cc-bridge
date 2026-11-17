"""Sums up a CSV from sample.py: memory and CPU per part, at the start, at the peak and at the end.

Usage: python3 benchmark/summarize.py [samples.csv]
"""
import collections
import csv
import sys


def main():
    rows = list(csv.DictReader(open(sys.argv[1] if len(sys.argv) > 1 else "samples.csv")))
    totals = collections.defaultdict(lambda: collections.defaultdict(lambda: [0, 0.0]))
    for r in rows:
        part = "commands the agents ran" if r["role"].startswith("tool:") else r["role"]
        totals[float(r["t"])][part][0] += int(r["rss_kb"])
        totals[float(r["t"])][part][1] += float(r["cpu"])
    times = sorted(totals)
    print(f"{len(times)} samples over {times[-1]:.0f} s; memory is RSS in MB, CPU in % of one core\n")
    print(f"{'part':26s} {'start':>7s} {'peak':>7s} {'end':>7s} {'peak CPU':>9s}")
    for part in sorted({p for t in times for p in totals[t]}):
        series = [totals[t][part] for t in times if part in totals[t]]
        rss = [kb / 1024 for kb, _ in series]
        end = rss[-10:]
        print(f"{part:26s} {rss[0]:7.0f} {max(rss):7.0f} {sum(end) / len(end):7.0f} {max(c for _, c in series):8.0f}%")


if __name__ == "__main__":
    main()
