#!/usr/bin/env python3
"""Compare paired candidate timings; never infer whole-system speed from this microbenchmark."""
import argparse
import csv
import json
import itertools
from pathlib import Path
import statistics

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    groups = {}
    fields = ["operation", "variant", "bytes", "offset", "pattern", "difference",
              "repetition", "iterations", "elapsed_ns", "vector_calls"]
    with args.input.open() as stream:
        reader = csv.DictReader(stream)
        if reader.fieldnames != fields:
            raise ValueError("unexpected CSV fields")
        for raw in reader:
            r = {key: int(value) for key, value in raw.items()}
            key = tuple(r[k] for k in ("operation", "bytes", "offset", "pattern", "difference"))
            if r["iterations"] <= 0 or r["elapsed_ns"] <= 0:
                raise ValueError("empty timing/work")
            expected = r["iterations"] if r["variant"] == 2 else 0
            if (r["variant"] == 4 and not 0 <= r["vector_calls"] <= r["iterations"]) or (r["variant"] != 4 and r["vector_calls"] != expected):
                raise ValueError("unexpected vector-entry count")
            rows = groups.setdefault(key, {}).setdefault(r["repetition"], {})
            if r["variant"] in rows:
                raise ValueError("duplicate pair")
            rows[r["variant"]] = r
    report = []
    for key, reps in sorted(groups.items()):
        if len(reps) < 3:
            raise ValueError("at least three repetitions required")
        variants = set(next(iter(reps.values())))
        if 0 not in variants or not variants <= {0,1,2,4} or len(variants)<2:
            raise ValueError("unsupported candidate matrix")
        for pair in reps.values():
            if set(pair) != variants or len({r["iterations"] for r in pair.values()}) != 1:
                raise ValueError("incomplete/unequal-work pair")
        for base, candidate in itertools.combinations(sorted(variants),2):
            if base not in variants or candidate not in variants:
                continue
            a = [r[base]["elapsed_ns"] / r[base]["iterations"] for r in reps.values()]
            b = [r[candidate]["elapsed_ns"] / r[candidate]["iterations"] for r in reps.values()]
            changes = [100 * (1-y/x) for x,y in zip(a,b)]
            report.append({
                "operation": key[0], "bytes": key[1], "offset": key[2],
                "pattern": key[3], "difference": key[4],
                "baseline_variant": base, "candidate_variant": candidate,
                "repetitions": len(reps), "baseline_ns": statistics.median(a),
                "candidate_ns": statistics.median(b),
                "paired_reduction_percent": statistics.median(changes),
                "paired_range_percent": [min(changes),max(changes)],
                "faster_pairs": sum(x>0 for x in changes),
            })
    args.output.write_text(json.dumps({
        "meaning": "full function with vector context entry/exit; excludes per-ioctl fixture setup; no whole-system claim",
        "comparisons": report,
    },indent=2)+"\n")
    print(f"PASS comparisons={len(report)} groups={len(groups)}")

if __name__ == "__main__":
    main()
