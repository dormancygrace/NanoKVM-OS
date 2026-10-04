#!/usr/bin/env python3
"""Resolve private kernel IP samples using the matching /proc/kallsyms snapshot."""
import argparse
import bisect
from collections import Counter
import csv
import json
from pathlib import Path

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("samples", type=Path)
    parser.add_argument("symbols", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    symbols = {}
    for line in args.symbols.read_text().splitlines():
        parts = line.split()
        if len(parts) >= 3 and parts[1] in ("t", "T", "w", "W") and not parts[2].startswith((".", "$")):
            address = int(parts[0], 16)
            if address:
                symbols[address] = " ".join(parts[2:])
    addresses = sorted(symbols)
    if not addresses:
        raise ValueError("nonzero kernel text symbol addresses required")
    counts = Counter()
    with args.samples.open() as stream:
        rows = csv.DictReader(stream)
        if rows.fieldnames != ["ip", "count"]:
            raise ValueError("invalid sample CSV header")
        for row in rows:
            ip, count = int(row["ip"], 16), int(row["count"])
            if count <= 0:
                raise ValueError("sample count must be positive")
            index = bisect.bisect_right(addresses, ip) - 1
            name = symbols[addresses[index]] if index >= 0 else "<unresolved>"
            counts[name] += count
    total = counts.total()
    if not total:
        raise ValueError("empty samples")
    report = {
        "samples": total,
        "meaning": "kernel instruction-pointer samples; includes idle/profiler overhead; no call-chain attribution",
        "functions": [{"function": name, "samples": count, "percent": count * 100 / total}
                      for name, count in counts.most_common()],
    }
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({**report, "functions": report["functions"][:25]}, indent=2))

if __name__ == "__main__":
    main()
