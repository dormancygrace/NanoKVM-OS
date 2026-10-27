# Component build profiles for SG2002/C906

NanoKVM-owned source builds use a component profile rather than one global
optimization level. These defaults follow measurements on the C906.

| Component | Compiler and optimization | Vectorization / LTO |
| --- | --- | --- |
| Linux and matching modules, including UAC | GCC 16.2, -O3, thead-c906, lp64 | Compiler vectorization off; LTO off |
| Media libraries, MMF/capture, JSON/miniz and kvm_system | GCC 16.2, -O2, thead-c906, lp64d | Compiler vectorization off |
| Go server/updater C/CGO objects | GCC -O2 source profile | Go compiler/runtime selection stays separate |
| Source-owned network tools and private musl | GCC -O2 source profile | Compiler vectorization off; stale -Os objects rebuilt |
| Opus, tinyalsa and usb-audio-capture | GCC -O2 audio profile, float Opus | Compiler vectorization off |
| zlib-ng (nanokvm-zlib, libz.so.1; static in NanoKVM-Server) | GCC -O2, thead-c906, lp64d, -mno-strict-align, without xtheadvector | Compiler vectorization off; RVV and Zbc code off |
| OpenSSL 3.5.9 LTS (nanokvm-openssl) | GCC -O3, thead-c906, lp64d, without xtheadvector | Compiler vectorization and LTO off; scalar inline memory operations; upstream crypto code |
| liblzma (xz, static in NanoKVM-Server) | GCC -O2, thead-c906, lp64d, without xtheadvector | Compiler vectorization off; the flags do not change its decoder speed (10.5 MB/s with or without) |
| Optional RustDesk performance build | Rust opt-level=3, scalar XTHead | fat LTO; codegen-units=1 |
| RustDesk generic release | Existing Rust release profile | Remains available |
| U-Boot/FIP and official Alpine packages (except zlib and OpenSSL) | Existing policies | Outside this optimization change |

Both C loop vectorization and SLP are explicitly disabled. The userspace ISA
still includes xtheadvector: existing vendor/intrinsic/assembly implementations
remain usable. Kernel ISA stays separate from userspace FP/vector ISA.

platform/cpu-profile.json supplies the defaults. The common flags/metadata
helper accepts userspace, kernel, audio, bootloader and openssl. Kbuild is pinned to
the GCC compiler and CONFIG_LTO_NONE; the command auditor rejects accidental
-flto options. Kernel PI/EFI/LZ4 and external JPEG/Realtek Makefiles inherit
the requested optimizer rather than appending -O2. CVI SYS/GDC and json-c
likewise inherit the userspace optimizer.

The C906 vector-context patch saves the outgoing vector state before restoring
the incoming FCSR. This preserves the hardware-observed VXRM/VXSAT aliasing
across task switches; handwritten RVV still requires correct kernel context
handling.

## RustDesk build

There is one build: the release profile is the C906 build. Use the
existing packaging builder with its required --linker, --apk, --output and
corresponding-source selection.

For a direct cross build from addons/rustdesk:

```sh
cargo build --locked --offline --release --target riscv64gc-unknown-linux-musl
```

The matching musl linker must be supplied through Cargo's target linker
setting. The packaging builder does this through --linker. The release profile
selects -O3, full LTO and one codegen unit; addons/rustdesk/.cargo/config.toml
carries the scalar T-Head target features and static CRT for this target and
for the native Alpine target riscv64-alpine-linux-musl. Keep the explicit
--target, which confines the flags to target code. This Rust backend does not
emit legacy RVV 0.7.1.

## Evidence and limits

- Kernel: neither GCC nor Clang, with or without LTO, is faster across the
  board. Clang Full LTO slowed system calls and loopback networking and sped
  up others, so the kernel stays on GCC -O3.
- Opus: scalar float -O2 uses the least CPU; the vector variants used more.
- RustDesk XSalsa20-Poly1305: most of the gain over generic -O2 comes from
  T-Head targeting, not from LTO.
- Other native components use scalar -O2 as the baseline; not every library
  was timed.
- Compiler flags do not select hardware AES/SHA or change TLS algorithms.

OpenSSL measurements and reproduction: [C906/OpenSSL 3.5 LTS](../firmware/crypto/experimental/openssl-c906/README.md).
