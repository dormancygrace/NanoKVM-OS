#!/usr/bin/env python3
"""Render the reviewed migration ledger without changing qualification statuses."""
import collections
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
docs = root / "docs/experiments/v3.0"
baseline = json.loads((docs / "routes-baseline.json").read_text())
routes = json.loads((docs / "routes-rust.json").read_text())
identity = lambda route: (route["method"], route["path"])
assert len(routes) == len(baseline) == 204
assert len({identity(route) for route in routes}) == len(routes)
assert [identity(route) for route in routes] == [identity(route) for route in baseline]
for original, reviewed in zip(baseline, routes):
    protected = {key: value for key, value in original.items() if key not in {"rust_status", "evidence"}}
    assert protected == {key: reviewed.get(key) for key in protected}, identity(original)
    assert reviewed["rust_status"] in {"pending", "partial-isolated", "implemented-isolated"}
    assert isinstance(reviewed["evidence"], list)
    assert reviewed["rust_status"] == "pending" or reviewed["evidence"] or reviewed.get("rust_evidence"), identity(reviewed)
counts = collections.Counter(route["rust_status"] for route in routes)
old_lines = (docs / "parity.md").read_text().splitlines()
assert old_lines.count("| Method | Path | Access | Go handler/source | Rust status | Evidence |") == 1
old_rows = {}
for line in old_lines:
    cells = [cell.strip() for cell in line.strip("|").split("|")]
    if len(cells) == 6 and cells[0] in {route["method"] for route in routes}:
        key = (cells[0], cells[1].strip("`"))
        assert key not in old_rows
        old_rows[key] = cells
assert set(old_rows) == {identity(route) for route in routes}
rows = old_lines[:old_lines.index("| Method | Path | Access | Go handler/source | Rust status | Evidence |")]
assert rows[0] == "# Functional parity matrix"
rows[2] = (f"{len(routes)} baseline registrations: {counts['implemented-isolated']} implemented in the isolated Rust slice, "
           f"{counts['partial-isolated']} partial, {counts['pending']} pending. This is source/host qualification, not complete hardware parity.")
rows += ["| Method | Path | Access | Go handler/source | Rust status | Evidence |", "|---|---|---|---|---|---|"]
for route in routes:
    cells = old_rows[identity(route)]
    expected_access = route["authorization"] + (" + input owner" if route["input_owner"] else "")
    assert cells[2] == expected_access and cells[3] == f"{route['source']}:{route['line']}"
    if cells[4] != route["rust_status"]:
        cells[5] = "; ".join(Path(item).name for item in route["evidence"] if item.endswith(".md")) or "—"
    cells[4] = route["rust_status"]
    rows.append("| " + " | ".join(cells) + " |")
(docs / "parity.md").write_text("\n".join(rows) + "\n")
print("Reviewed isolated implementations:", counts['implemented-isolated'], "; partial:", counts['partial-isolated'], "; pending:", counts['pending'])
