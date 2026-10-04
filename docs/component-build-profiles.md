# Component build profiles for SG2002/C906

NanoKVM-owned source builds use a component profile rather than one global
optimization level. These defaults follow the C906 measurements made on
2026-10-04.

| Component | Compiler and optimization | Vectorization / LTO |
| --- | --- | --- |
| Linux and matching modules, including UAC | GCC 16.2, -O3, thead-c906, lp64 | Compiler vectorization off; LTO off |
| Media libraries, MMF/capture, JSON/miniz and kvm_system | GCC 16.2, -O2, thead-c906, lp64d | Compiler vectorization off |
| Go server/updater C/CGO objects | GCC -O2 source profile | Go compiler/runtime selection stays separate |
| Source-owned network tools and private musl | GCC -O2 source profile | Compiler vectorization off; stale -Os objects rebuilt |
| Opus, tinyalsa and usb-audio-capture | GCC -O2 audio profile, float Opus | Compiler vectorization off |
| Optional RustDesk performance build | Rust opt-level=3, scalar XTHead | fat LTO; codegen-units=1 |
| RustDesk generic release | Existing Rust release profile | Remains available |
| U-Boot/FIP and official Alpine packages | Existing policies | Outside this optimization change |

Both C loop vectorization and SLP are explicitly disabled. The userspace ISA
still includes xtheadvector: existing vendor/intrinsic/assembly implementations
remain usable. Kernel ISA stays separate from userspace FP/vector ISA.

platform/cpu-profile.json supplies the defaults. The common flags/metadata
helper accepts userspace, kernel, audio and bootloader. Kbuild is pinned to
the GCC compiler and CONFIG_LTO_NONE; the command auditor rejects accidental
-flto options. Kernel PI/EFI/LZ4 and external JPEG/Realtek Makefiles inherit
the requested optimizer rather than appending -O2. CVI SYS/GDC and json-c
likewise inherit the userspace optimizer.

The C906 vector-context patch saves the outgoing vector state before restoring
the incoming FCSR. This preserves the hardware-observed VXRM/VXSAT aliasing
across task switches; handwritten RVV still requires correct kernel context
handling.

## RustDesk selection

Use the existing packaging builder with --build-profile performance, together
with its required --linker, --apk, --output and corresponding-source selection.
Use --build-profile release for the generic alternative. Separate output
directories prevent either artifact from overwriting the other.

For a direct cross build from addons/rustdesk:

```sh
cargo --config performance.toml build --locked --offline \
  --profile performance --target riscv64gc-unknown-linux-musl
```

The matching musl linker must be supplied through Cargo's target linker
setting. The packaging builder does this through --linker. performance.toml
contains the scalar T-Head target features and static CRT setting; this Rust
backend does not emit legacy RVV 0.7.1.

## Evidence and limits

- The kernel comparison used 19 cases, three repetitions per batch, with
  GCC -> Clang Full LTO -> GCC -> Clang no-LTO -> GCC, identical userspace
  and 1 GHz. There were 285 timing rows. Relative to GCC -O3, Clang Full
  LTO increased getpid latency by 26.3%, pipe768 by 28.6% and UDP loopback
  by 4.0–6.5%; getrandom4KiB and tmpfs open/close improved by 14.8% and
  27.7%. There is no universal kernel-speed winner. GCC versus Clang also
  includes compiler-capability and tune differences.
- Opus scalar -O2 float used 8.32% of one core on the measured tone; scalar
  -O3 float used 8.51%, and vector float variants used 11.28–13.05%.
  The vector -O3 fixed variant had an unresolved signal differential.
- RustDesk XSalsa20-Poly1305 on 16KiB reached 18.55 MiB/s with the performance
  profile versus 13.33 MiB/s for generic -O2. Scalar T-Head -O2 already
  reached 18.43 MiB/s: most of the gain comes from T-Head targeting, and
  fat LTO is not a universal improvement for session setup.
- The remaining native/system per-component winners are not established;
  -O2 scalar is the baseline, not a claim that every library was timed.
- Hardware AES/SHA dispatch and small-packet fallback are runtime integration
  work. This change does not enable a new engine/provider or alter TLS
  algorithms based on compiler flags.

Existing reference output checksums describe the earlier firmware artifacts.
Regenerate and qualify the complete release output set before replacing those
references. This PR changes source build recipes; it does not publish APKs,
replace release images or deploy new userspace to the stand.
