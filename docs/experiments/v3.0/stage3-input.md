# Stage 3 input/settings slice

This is a bounded implementation milestone; the complete HID/USB/WebSocket stage is still in progress. Production activation remains blocked.

## Implemented
- Seven HID APIs: mode and input availability reads, shortcut list/add/delete, leader-key get/set. Role and factory-password gates run before handlers. `/etc/kvm/shortcuts.json` and `/etc/kvm/leader-key` retain existing fields, UUID v4 identifiers and 0644 permissions; writes serialize and replace atomically. Legacy torn shortcut JSON recovers to an empty set, as Go does. Missing leader-key reset is idempotent. Unknown `bcdDevice` fails; configured network/storage/audio functions take precedence over a stale HID-only flag.
- Input ownership core with a random 128-bit secret per socket, view-only opt-out, explicit transfer, promotion only when a single eligible viewer remains, external add-on reservation and release-before-transfer ordering. Bounded watch notifications retain latest status. Generation tickets reject queued reports from a previous ownership period; execution serializes against transitions. Lease comparison uses RustCrypto ctutils already present in the resolved crypto dependency tree.
- Existing binary HID frame decoding: eight-byte keyboard, legacy/new relative 4/5 bytes, absolute 6/7 bytes, heartbeat/control requests. Keyboard held-state excludes the reserved byte. Absolute release retains position; Windows pen/auxiliary reports preserve pointer descriptor semantics. Nonblocking write helper bounds retries and rejects partial reports.

## Qualification
The suite now has 24 tests (9 unit, 15 API contracts), including isolated persisted settings/permissions, viewer/admin access, legacy mouse and Windows descriptor vectors, release positions, deadline/short-write handling, stale ownership tickets, external cleanup and remaining-viewer policy. Host and generic riscv64 musl/QEMU qualification is recorded separately in stage3-qualification.json after the run completes. No shared-device writes.

## Pending integration
The ownership core and HID framing are **not yet connected to live WebSockets or device descriptors**. `/api/ws`, paste, HID LEDs, HID mode changes, reset/recover, USB composition and all media remain pending. Do not infer working keyboard/mouse input from the settings APIs or model tests. Next wire authenticated, origin-checked, revocable sockets to bounded report workers; maintain 4 KiB input messages, 90-second heartbeat, 10-second writes and close code 4401. Integrate manual/MCP/PicoClaw arbitration, jiggler, queue-failure cleanup, LED reader lifecycle, USB/gadget reopening and external RustDesk ownership before claiming stage 3 parity. Hardware and performance gates still need the agreed stand window.
