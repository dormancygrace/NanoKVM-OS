#!/usr/bin/env python3
"""Summarize two NanoKVM Alpine timing directories."""
from __future__ import annotations

import argparse
import csv
import statistics
from pathlib import Path


def load(path: Path) -> dict[str, list[dict[str, str]]]:
    timings = path / "timings.tsv"
    grouped: dict[str, list[dict[str, str]]] = {}
    with timings.open(newline="", encoding="utf-8") as stream:
        for row in csv.DictReader(stream, delimiter="\t"):
            grouped.setdefault(row["workload"], []).append(row)
    if not grouped:
        raise SystemExit(f"no timings in {timings}")
    return grouped


def median(rows: list[dict[str, str]], field: str) -> float:
    return statistics.median(float(row[field]) for row in rows)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("stock", type=Path)
    parser.add_argument("tuned", type=Path)
    parser.add_argument("--tsv", type=Path)
    args = parser.parse_args()

    stock = load(args.stock)
    tuned = load(args.tuned)
    if stock.keys() != tuned.keys():
        raise SystemExit("stock and tuned workload sets differ")

    result = []
    for workload in stock:
        if len(stock[workload]) != len(tuned[workload]):
            raise SystemExit(f"iteration count differs for {workload}")
        s_real = median(stock[workload], "real_s")
        t_real = median(tuned[workload], "real_s")
        s_user = median(stock[workload], "user_s")
        t_user = median(tuned[workload], "user_s")
        result.append({
            "workload": workload,
            "iterations": len(stock[workload]),
            "stock_real_s": s_real,
            "tuned_real_s": t_real,
            "real_change_pct": (t_real / s_real - 1.0) * 100.0,
            "speedup": s_real / t_real,
            "stock_user_s": s_user,
            "tuned_user_s": t_user,
            "user_change_pct": (t_user / s_user - 1.0) * 100.0 if s_user else 0.0,
        })

    if args.tsv:
        args.tsv.parent.mkdir(parents=True, exist_ok=True)
        with args.tsv.open("w", newline="", encoding="utf-8") as stream:
            writer = csv.DictWriter(stream, fieldnames=result[0].keys(), delimiter="\t")
            writer.writeheader()
            writer.writerows(result)

    print("| Workload | n | Stock median, s | C906 median, s | Wall change | Speedup | User CPU change |")
    print("|---|---:|---:|---:|---:|---:|---:|")
    for row in result:
        print(
            f"| {row['workload']} | {row['iterations']} | "
            f"{row['stock_real_s']:.3f} | {row['tuned_real_s']:.3f} | "
            f"{row['real_change_pct']:+.2f}% | {row['speedup']:.3f}x | "
            f"{row['user_change_pct']:+.2f}% |"
        )


if __name__ == "__main__":
    main()
