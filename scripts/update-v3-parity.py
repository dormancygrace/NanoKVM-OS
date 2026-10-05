#!/usr/bin/env python3
"""Render the reviewed Rust migration ledger; never claim hardware parity."""
import json
from pathlib import Path
root = Path(__file__).resolve().parents[1]
docs = root / "docs/experiments/v3.0"
routes = json.loads((docs / "routes-baseline.json").read_text())
ported = {
    ("POST", "/api/auth/login"), ("GET", "/api/auth/password"), ("GET", "/api/auth/account"),
    ("POST", "/api/auth/password"), ("POST", "/api/auth/logout"),
    ("GET", "/api/auth/users"), ("POST", "/api/auth/users"),
    ("PUT", "/api/auth/users/:username"), ("DELETE", "/api/auth/users/:username"),
    ("POST", "/api/auth/users/:username/password"),
    ("GET", "/api/branding"), ("GET", "/api/branding/logo"), ("GET", "/api/branding/favicon"),
    ("GET", "/api/vm/web-title"), ("POST", "/api/vm/web-title"),
    ("GET", "/api/hid/mode"), ("GET", "/api/hid/input-status"),
    ("POST", "/api/hid/mode"), ("POST", "/api/hid/reset"),
    ("POST", "/api/internal/usb/recover"),
    ("GET", "/api/vm/device/virtual"), ("POST", "/api/vm/device/virtual"), ("PUT", "/api/vm/device/virtual"),
    ("GET", "/api/hid/leds"),
    ("GET", "/api/vm/mouse-jiggler"), ("POST", "/api/vm/mouse-jiggler/"),
    ("GET", "/api/hid/shortcuts"), ("POST", "/api/hid/shortcut"), ("DELETE", "/api/hid/shortcut"),
    ("GET", "/api/hid/shortcut/leader-key"), ("POST", "/api/hid/shortcut/leader-key"),
}
assert ported <= {(r["method"], r["path"]) for r in routes}
partial = {("GET", "/api/ws")}
rows = ["# Functional parity matrix", "",
        f"{len(routes)} baseline registrations: {len(ported)} implemented in the isolated Rust slice, {len(partial)} partial, {len(routes)-len(ported)-len(partial)} pending. This is source/host qualification, not complete hardware parity.", "",
        "Owner OS password synchronization cannot run in an isolated root; its rollback path is tested. Internal routes require their loopback credential; pending handlers return 501 after that gate. MCP API-key routes fail closed. Pending public/session/admin routes return HTTP 501 after their access gate. All nonpublic baseline routes have an unauthenticated protection test.", "",
        "| Method | Path | Access | Go handler/source | Rust status | Evidence |",
        "|---|---|---|---|---|---|"]
for r in routes:
    if (r["method"], r["path"]) in partial:
        r["rust_status"] = "partial-isolated"
        r["evidence"] = ["server-rust/src/ws.rs", "docs/experiments/v3.0/stage3-coordinator.md"]
    if (r["method"], r["path"]) in ported:
        r["rust_status"] = "implemented-isolated"
        r["evidence"] = (["server-rust/src/hid_settings.rs", "docs/experiments/v3.0/stage3-input.md"]
                         if r["path"].startswith("/api/hid/") else
                         ["server-rust/src/api.rs", "docs/experiments/v3.0/validation.md"])
    evidence = ("stage3-input.md; contract/filesystem" if r["path"].startswith("/api/hid/")
                else "validation.md; API/contract/UI slice") if r["evidence"] else "—"
    if (r["method"], r["path"]) in partial:
        evidence = "stage3-coordinator.md; real sockets/HID/LED/ownership; media snapshots and full addons pending"
    if r['path'] == '/api/hid/leds':
        r['evidence'] = ['server-rust/src/leds.rs', 'docs/experiments/v3.0/stage3-leds.md']
        evidence = 'stage3-leds.md; real descriptor/REST/socket snapshots'
    if r['path'].startswith('/api/vm/mouse-jiggler'):
        r['evidence'] = ['server-rust/src/jiggler.rs', 'docs/experiments/v3.0/stage3-jiggler.md']
        evidence = 'stage3-jiggler.md; settings/admin/actual timer/priority/compensation'
    if (r['method'], r['path']) in {('POST', '/api/hid/mode'), ('POST', '/api/hid/reset')}:
        r['evidence'] = ['server-rust/src/usb.rs', 'docs/experiments/v3.0/stage3-usb.md']
        evidence = 'stage3-usb.md; injected actions/reopen/ownership/symlink/response ordering'
    if r['path'] == '/api/internal/usb/recover':
        r['evidence'] = ['server-rust/src/internal.rs', 'server-rust/src/usb.rs', 'docs/experiments/v3.0/stage3-internal-usb.md']
        evidence = 'stage3-internal-usb.md; actual-peer auth/HTTP exception/injected recovery'
    if r['path'] == '/api/vm/device/virtual':
        r['evidence'] = ['server-rust/src/composition.rs', 'server-rust/src/usb.rs', 'server-rust/src/monitor.rs', 'docs/experiments/v3.0/stage3-composition.md']
        evidence = 'stage3-composition.md; actual-Go budget/EDID; injected native rebind/rollback'
    rows.append(f"| {r['method']} | `{r['path']}` | {r['authorization']}" +
                (" + input owner" if r["input_owner"] else "") +
                f" | {r['source']}:{r['line']} | {r['rust_status']} | {evidence} |")
(docs / "routes-rust.json").write_text(json.dumps(routes, indent=2) + "\n")
(docs / "parity.md").write_text("\n".join(rows) + "\n")
print("Rust isolated implementations:", len(ported), "; partial:", len(partial), "; pending:", len(routes) - len(ported) - len(partial))
