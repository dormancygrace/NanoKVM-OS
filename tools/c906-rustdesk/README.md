# Finite RustDesk relay acceptance

This fixture binds only 127.0.0.1:32317, accepts two bounded RustDesk relay
request frames and forwards their subsequent bytes. It closes after one pair,
30 seconds of forwarding, or an absolute 40-second process deadline. The
maximum request and each direction's forwarding buffer are 64 KiB.
It neither decrypts nor changes the application frames.

In a coordinated test slot, cross-build `loopback-relay.c` with the target
compiler, `-static -O2 -Wall -Wextra -Werror`. Cross-build the existing ignored
interop test with the same scalar profile and static musl linker:

```sh
cargo --config performance.toml test --locked --no-run --profile performance \
  --target riscv64gc-unknown-linux-musl --bin nanokvm-rustdesk
```

Run both the fixture and that test executable on the target. Use the normal
`RUSTDESK_TEST_HBBS`, `RUSTDESK_TEST_ID`, `RUSTDESK_TEST_DATA`,
`RUSTDESK_TEST_KEY` and `RUSTDESK_TEST_PASSWORD` interop environment; set
`RUSTDESK_TEST_RELAY=127.0.0.1:32317`. Select
`rendezvous::tests::official_hbbs_hbbr_secure_interop --ignored --exact`.
Official rendezvous registration and signed/encrypted login still occur;
only the session's relay data path uses this temporary local fixture.
The installed service configuration is not edited.

The test requests view-only H.264/H.265 without audio or HID events, uses
finite protocol keepalives, and checks 120 nonempty encrypted video messages.
The C906 production candidate passed this hardware-side test in 4.92 seconds
including setup, forwarding 2,147,489 video-direction bytes. This proves
functional sustained framing on real HDMI data in this finite test; it does
not measure external relay throughput or application FPS.

Keep executables, credentials, captures and raw results outside the public
repository. Close test processes and remove target RAM assets after testing.
