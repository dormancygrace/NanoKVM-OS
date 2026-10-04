# Action journal

## 2026-10-04T22:15:47.481504+00:00 — Stage 1 start
Before: user-authorized isolated development; verify worktree/instructions before mutations.
Actions: read parent AGENTS.md; git status --short; git branch --show-current; git rev-parse HEAD; inspect server/go.mod, main.go, Makefile, nanokvm-app APKBUILD and integration/build references.
Results: clean v3.0-experimental at a53b25579ab87cc98f85323b4743deb0d4da907b; native capture retained; existing RustDesk bridge exposes a hidden Go runtime dependency that must be replaced. Rust target present.
Next: inventory every router group/endpoint, auth/session boundary, executable/helper, persistent state and native API before selecting implementation boundaries.

## 2026-10-04T22:18:59.622171+00:00 — Inventory and architecture, before
Generate deterministic inventory of all Go route registrations, modules, process invocation sites and absolute path literals; verify all router calls resolve and retain authorization annotations for manual review. Write architecture decisions and route parity ledger. Inspect the unchanged baseline build outputs to locate toolchains without changing another worktree. Next implement the isolated Rust auth/TLS vertical slice.

## 2026-10-04T22:21:06.402085+00:00 — Stage 1 source inventory, after
Checks: python3 scripts/inventory-v3.py; every router registration resolved; duplicate method/path check passed. Result: 204 registrations, 277 source files, 44,250 source lines, 82 exec invocation sites, 227 filesystem literals. Found four root-only RustDesk IPC sockets and two production Go update helpers. Parent AGENTS.md is the only applicable repository instruction file. First inventory regex failed on chained group middleware; fixed with balanced .Use parsing and reran successfully. Primary library/target docs reviewed and linked in architecture.md. Existing matched toolchain/native/UI outputs located under parent work/v2.1-b1-20261004/platform; read-only reuse permitted. Next: commit inventory milestone, implement auth/config/TLS with isolated root, resolve and prove dependencies on riscv64 musl.
