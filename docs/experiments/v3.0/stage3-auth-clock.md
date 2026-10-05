# Authentication clock and WebSocket queue repair

Authenticated requests and token creation now return controlled errors when the system clock precedes the Unix epoch. They no longer panic or wrap the time. Explicitly disabled authentication remains supported. WebSocket expiry timers are rescheduled in intervals of at most one hour, so distant valid claims cannot overflow an Instant.

Socket registration, fresh account validation, control changes and cleanup share a dedicated two-job lane. Long HTTP operations can no longer block expiry detection or control release. A socket remains registered until cleanup is admitted, so global shutdown still releases queued sessions. HID neutralization completes before the 4401 close frame.

Qualification: 144 tests on host and generic riscv64 musl/QEMU (48 unit, 27 API, five dashboard, nine CPU, eight time, three identity, three memory, 21 real WebSocket/paste, nine USB/monitor, eleven GPIO/system). A real socket releases control, answers heartbeat and closes with4401 while all four API execution permits are held. Existing account revocation, blocked HID and shutdown cases pass. fmt, all-target clippy, static release and certificate-verified TLS/redirect/cookie/shutdown checks pass. See stage3-auth-clock-qualification.json for hashes and timings.

These are isolated runtime tests, not device input/media/performance qualification. The full Go replacement remains incomplete. No device configuration, system clock, hardware input or production runtime was changed.
