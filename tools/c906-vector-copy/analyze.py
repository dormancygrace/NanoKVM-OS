#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Compare alternating, paired scalar/vector CSV rows; write private JSON."""
import argparse
import csv
import json
import statistics
from collections import defaultdict
from pathlib import Path

OPERATIONS = ("from_user", "to_user", "copy", "fill")
METRICS = ("elapsed_ns", "call_wall_ns", "call_cpu_ns")


def compare(path):
    groups = defaultdict(dict)
    with path.open(newline="") as stream:
        for raw in csv.DictReader(stream):
            row = {key: int(value) for key, value in raw.items()}
            key = tuple(row[name] for name in
                        ("operation", "bytes", "src_offset", "dst_offset"))
            operation, size, _, _ = key
            mode, repetition = row["vector"], row["repetition"]
            if operation not in range(4) or mode not in (0, 1):
                raise ValueError("invalid operation/mode")
            if not 0 < size <= 1048576 or row["iterations"] <= 0:
                raise ValueError("invalid work count")
            if row["vector_calls"] != (row["iterations"] if mode else 0):
                raise ValueError("missing vector execution")
            pair_key = (repetition, mode)
            if pair_key in groups[key]:
                raise ValueError("duplicate paired row")
            if any(row[metric] <= 0 for metric in METRICS):
                raise ValueError("invalid timing")
            groups[key][pair_key] = row
    results = []
    for key, rows in sorted(groups.items()):
        repeats = sorted({repetition for repetition, _ in rows})
        if len(repeats) < 3:
            raise ValueError("at least three paired repetitions required")
        for repeat in repeats:
            if (repeat, 0) not in rows or (repeat, 1) not in rows:
                raise ValueError("incomplete scalar/vector pair")
            if rows[repeat, 0]["iterations"] != rows[repeat, 1]["iterations"]:
                raise ValueError("unequal paired work")
        result = dict(zip(("operation", "bytes", "src_offset", "dst_offset"), key))
        result["operation"] = OPERATIONS[key[0]]
        result["paired_repetitions"] = len(repeats)
        result["metrics"] = {}
        for metric in METRICS:
            scalar = [rows[r, 0][metric] / rows[r, 0]["iterations"] for r in repeats]
            vector = [rows[r, 1][metric] / rows[r, 1]["iterations"] for r in repeats]
            reductions = [100 * (s - v) / s for s, v in zip(scalar, vector)]
            result["metrics"][metric] = {
                "scalar_ns_median": statistics.median(scalar),
                "scalar_ns_range": [min(scalar), max(scalar)],
                "vector_ns_median": statistics.median(vector),
                "vector_ns_range": [min(vector), max(vector)],
                "paired_reduction_percent_median": statistics.median(reductions),
                "paired_reduction_percent_range": [min(reductions), max(reductions)],
                "vector_faster_pairs": sum(v < s for s, v in zip(scalar, vector)),
            }
        results.append(result)
    if not results:
        raise ValueError("no timing rows")
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("csv", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    data = {"input": str(args.csv), "comparisons": compare(args.csv),
            "metric_scope": {
                "elapsed_ns": "kernel copy loop, including vector context and scheduling",
                "call_wall_ns": "complete ioctl wall time, including buffer preparation",
                "call_cpu_ns": "complete ioctl thread CPU time, including buffer preparation"},
            "limits": "Microbenchmarks do not establish whole-system gains."}
    args.output.write_text(json.dumps(data, indent=2) + "\n")


if __name__ == "__main__":
    main()
