#!/usr/bin/env python3
"""Summarize a private CSV; never copy raw device evidence into a public tree."""
import argparse
import csv
import json
import math
import statistics
from collections import defaultdict
from pathlib import Path


def analyze(path):
    samples = defaultdict(dict)
    outputs = defaultdict(set)
    with path.open() as stream:
        for row in csv.DictReader(stream):
            fields = {key: int(value) for key, value in row.items()}
            key = tuple(fields[k] for k in ("bytes", "pattern", "offset", "operation", "repetition"))
            samples[key][fields["variant"]] = fields["elapsed_ns"] / fields["iterations"]
            outputs[key].add(fields["output"])
    variants = sorted({variant for value in samples.values() for variant in value})
    if not samples or variants != list(range(len(variants))) or any(len(v) != len(variants) for v in samples.values()):
        raise ValueError("incomplete candidate matrix")
    if any(len(v) != 1 for v in outputs.values()):
        raise ValueError("compressed size differs between candidates")
    result = []
    for size in sorted({key[0] for key in samples}):
        for operation in (0, 1):
            for offset in (None, 0):
                selected = [(key, value) for key, value in samples.items()
                            if key[0] == size and key[3] == operation and (offset is None or key[2] == offset)]
                for variant in variants[1:]:
                    for reference in (0, 1):
                        ratio = [value[variant] / value[reference] for _, value in selected]
                        result.append({"bytes": size, "operation": "decompress" if operation else "compress",
                                       "offset": "all" if offset is None else offset,
                                       "variant": variant, "reference": reference,
                                       "paired_rows": len(ratio),
                                       "median_time_change_pct": 100 * (statistics.median(ratio) - 1),
                                       "geomean_time_change_pct": 100 * (math.exp(statistics.mean(map(math.log, ratio))) - 1),
                                       "p90_time_change_pct": 100 * (sorted(ratio)[math.ceil(.9 * len(ratio)) - 1] - 1)})
    details = []
    for pattern in range(8):
        for operation in (0, 1):
            for variant in variants[1:]:
                selected = [value for key, value in samples.items()
                            if key[:3] == (4096, pattern, 0) and key[3] == operation]
                details.append({"pattern": pattern, "operation": operation, "variant": variant,
                                "production_ns": statistics.median(value[0] for value in selected),
                                "identity_ns": statistics.median(value[1] for value in selected),
                                "candidate_ns": statistics.median(value[variant] for value in selected),
                                "paired_change_vs_identity_pct": 100 * (statistics.median(value[variant] / value[1] for value in selected) - 1)})
    return {"rows": len(samples) * len(variants), "summaries": result, "page_aligned_details": details}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("csv", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    args.output.write_text(json.dumps(analyze(args.csv), indent=2) + "\n")
