# NanoKVM firmware sources

Sources and records that the platform build uses, next to the code in
`support/` and the build scripts in `platform/` and `scripts/`. How to build is
in [docs/BUILD.md](../docs/BUILD.md) and [platform/README.md](../platform/README.md).

| Directory | Contents |
|---|---|
| [alpine/](alpine/README.md) | Alpine base: OpenRC services, `/etc` files, compatibility scripts, package lists and signing keys |
| [boards/](boards/README.md) | SG2002 device trees for alpha, beta and PCIe, and the first-boot board detection |
| [buildroot/](buildroot/README.md) | Buildroot external tree: cross toolchain, host tools and initramfs utilities only |
| `cpu/` | C906 assembly for Go's TLS ChaCha20-Poly1305 and `bytes.Index`, and opt-in Go sysmon policies |
| `crypto/` | cryptodev-linux with the NanoKVM patch, and SG2002 crypto benchmarks |
| `mpi/` | Media library compatibility code and the vendor ISP object list |
| `probes/` | Capture and encoder probes built by `scripts/build-capture-qualification.py` |
| `sensor/` | LT6911 HDMI receiver driver patches |
| `services/` | Serial console and serial bridge services with their tests |
| `sources/` | BusyBox 1.36.1 source, kept for the GPL while v2.0-b4 is supported |
| `toolchain/` | T-Head instruction probe and profile |
| `sources.json` | Pinned upstream sources |

## C906 notes

- The C906 implements XTheadVector, the T-Head draft vector extension, not
  RVV 1.0. Code built for standard RVV must not run on it.
- Every NanoKVM-built C/C++ component uses `-mno-fence-tso`: the C906 raises
  SIGILL on `fence.tso`. The flags come from `platform/cpu-profile.json`
  through `scripts/nanokvm_cpu_profile.py`, and
  `scripts/audit-kbuild-profile.py` checks the kernel build for it. See the
  CPU profile section of [docs/BUILD.md](../docs/BUILD.md).

The notes on the earlier Buildroot and Linux 7.2.3 port that used to be here
are in the Git history of this file.
