#!/usr/bin/env python3
"""Compare matching mode-0 function measurements across kernel A/B/A boots."""
import argparse
import csv
import hashlib
import json
import statistics
from pathlib import Path

PHASES = ("A1", "B", "A2")


def read_csv(path, kind):
    fields = (("operation", "bytes", "src_offset", "dst_offset")
              if kind == "copy" else
              ("operation", "bytes", "offset", "pattern", "difference"))
    mode = "vector" if kind == "copy" else "variant"
    groups = {}
    with path.open(newline="") as stream:
        reader = csv.DictReader(stream)
        required = set(fields) | {mode, "repetition", "iterations", "elapsed_ns"}
        if not required.issubset(reader.fieldnames or []):
            raise ValueError(f"{path.name}: missing required CSV fields")
        for line, row in enumerate(reader, 2):
            try:
                values = {field: int(row[field]) for field in required}
            except (ValueError, TypeError) as error:
                raise ValueError(f"{path.name}:{line}: invalid integer") from error
            if values[mode] != 0:
                raise ValueError(f"{path.name}:{line}: expected running-kernel mode 0")
            allowed_operations = (0, 1) if kind == "copy" else (0,)
            if values["operation"] not in allowed_operations:
                raise ValueError(f"{path.name}:{line}: unexpected operation")
            if any(values[field] < 0 for field in required):
                raise ValueError(f"{path.name}:{line}: negative value")
            if not values["iterations"] or not values["elapsed_ns"]:
                raise ValueError(f"{path.name}:{line}: empty measurement")
            key = tuple(values[field] for field in fields)
            rep = values["repetition"]
            group = groups.setdefault(key, {})
            if rep in group:
                raise ValueError(f"{path.name}:{line}: duplicate workload/repetition")
            group[rep] = (values["elapsed_ns"] / values["iterations"],
                          values["iterations"])
    if not groups:
        raise ValueError(f"{path.name}: empty CSV")
    return fields, groups


def analyze(kind, paths):
    loaded = [read_csv(path, kind) for path in paths]
    fields = loaded[0][0]
    data = dict(zip(PHASES, (item[1] for item in loaded)))
    if not (set(data["A1"]) == set(data["B"]) == set(data["A2"])):
        raise ValueError("A1/B/A2 workload sets differ")
    expected_reps = None
    reports = []
    for key in sorted(data["B"]):
        groups = {phase: data[phase][key] for phase in PHASES}
        reps = set(groups["A1"])
        if any(set(groups[phase]) != reps for phase in PHASES):
            raise ValueError(f"{key}: A1/B/A2 repetition sets differ")
        if len(reps) < 3:
            raise ValueError(f"{key}: at least three repetitions required")
        if expected_reps is None:
            expected_reps = reps
        if reps != expected_reps:
            raise ValueError(f"{key}: incomplete workload repetitions")
        iterations = {value[1] for group in groups.values() for value in group.values()}
        if len(iterations) != 1:
            raise ValueError(f"{key}: mixed iteration counts")
        samples = {phase: [groups[phase][rep][0] for rep in sorted(reps)]
                   for phase in PHASES}
        medians = {phase: statistics.median(values)
                   for phase, values in samples.items()}
        reductions = {phase: 100 * (1 - medians["B"] / medians[phase])
                      for phase in ("A1", "A2")}
        faster = {phase: sum(groups["B"][rep][0] < groups[phase][rep][0]
                            for rep in reps) for phase in ("A1", "A2")}
        row = dict(zip(fields, key))
        row.update({
            "repetitions": len(reps),
            "iterations_per_row": next(iter(iterations)),
            "ns_per_operation_median": medians,
            "ns_per_operation_range": {phase: [min(values), max(values)]
                                       for phase, values in samples.items()},
            "time_reduction_percent_vs_baseline": reductions,
            "conservative_time_reduction_percent": min(reductions.values()),
            "baseline_latency_drift_percent": 100 * (medians["A2"] / medians["A1"] - 1),
            "faster_repetitions_vs_baseline": faster,
            "faster_than_both_repetitions": sum(
                groups["B"][rep][0] < min(groups["A1"][rep][0], groups["A2"][rep][0])
                for rep in reps),
        })
        if kind == "copy":
            row["different_word_alignment"] = key[2] != key[3]
        reports.append(row)
    return {
        "schema": 1,
        "kind": kind,
        "input_sha256": {phase: hashlib.sha256(path.read_bytes()).hexdigest()
                         for phase, path in zip(PHASES, paths)},
        "rows_per_phase": {phase: sum(map(len, groups.values()))
                           for phase, groups in data.items()},
        "workloads": len(reports),
        "metric": "elapsed_ns / iterations; kernel wrapper and common scheduling point included",
        "positive_percent_means": "candidate takes less time than the specified baseline",
        "limits": ("Serial A/B/A microbenchmarks; matching repetition numbers are descriptive, "
                   "not randomized pairs or confidence intervals. CSV alone does not verify "
                   "boot identity, correctness, state preservation, or whole-system gains."),
        "groups": reports,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=("copy", "csum"))
    parser.add_argument("a1", type=Path)
    parser.add_argument("candidate", type=Path)
    parser.add_argument("a2", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    paths = [args.a1, args.candidate, args.a2]
    try:
        if args.output.resolve() in [path.resolve() for path in paths]:
            raise ValueError("output must not overwrite an input CSV")
        report = analyze(args.kind, paths)
        args.output.write_text(json.dumps(report, indent=2) + "\n")
    except (ValueError, OSError) as error:
        parser.error(str(error))


if __name__ == "__main__":
    main()
