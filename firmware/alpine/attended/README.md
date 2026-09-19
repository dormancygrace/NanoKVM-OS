# Request-driven NanoKVM Alpine images

`scripts/build-alpine-personal-image.sh` is the backend primitive for an
Attended Sysupgrade style service. It accepts one explicit package request and
produces a complete update bundle. It has no HTTP server, queue, background
timer, A/B state or rollback policy. The separate frontend validates a request
and invokes this command as an isolated build job.

`scripts/serve-alpine-personal-builder.py` is the small synchronous HTTP
frontend for that primitive. It exposes `POST /v1/builds`, validates and
canonicalizes the requested profile and APK names, serializes build execution,
serves both signed repositories to the chroot, and returns hash-addressed
artifact URLs. Repeating an identical request returns the completed cached
bundle. It binds to `127.0.0.1` by default so a deployment can place its normal
authenticated reverse proxy in front of it without embedding account handling
in the image builder.

The NanoKVM server exposes a separate Alpine update flow in the existing admin
Updates page. Configure the reverse-proxied builder origin in
`/etc/kvm/server.yaml`:

```yaml
alpine:
  builderURL: https://builder.example
```

The device sends the selected profile and the editable current APK world to the
builder. It then downloads only the fixed artifact allowlist from relative URLs
on the same origin, enforces per-file size limits, checks every `SHA256SUMS`
entry, and calls `/usr/sbin/nanokvm-stage-update`. Build and download run in the
background while the existing three-second UI poll reports state. Activation is
a separate confirmed action which installs the one-shot recovery FIT and
reboots. The legacy `.nkos` controls are hidden when an Alpine profile is
installed because that updater targets the old Buildroot/ext4 layout.

## Inputs and trust

The base input is a clean Alpine riscv64 minirootfs or root tarball pinned by
SHA256. The normal NanoKVM FIT is pinned independently. APK verifies repository
indexes and packages with the official keys from the base root plus each public
key passed with `--repo-key`.

Repository order in the generated root is:

1. the C906 repository for the `c906-scalar` profile;
2. the NanoKVM repository containing `nanokvm-release` and its dependencies;
3. Alpine `main`;
4. Alpine `community`.

The NanoKVM packages have unique names. Tuned replacements retain their Alpine
package names and use the tested higher `pkgrel`; packages missing from the two
local repositories resolve normally from Alpine. A changing Alpine branch URL
does not provide byte-for-byte reproducibility by itself. Production should
point `--alpine-main` and `--alpine-community` at immutable mirrored snapshots.
The output records the exact effective repository file and every installed
package version. The HTTP service also hashes the current remote Alpine
`APKINDEX.tar.gz` files, both local repository indexes, the public keys and all
image-building inputs into the build ID. A repeated request is cached only while
those inputs are unchanged, so a new Alpine index produces a new personal image.

## Package request format

Package requests contain unversioned APK names, one per line, or repeated
`--package` arguments. Names are limited to lowercase APK name characters;
options, version expressions, shell syntax and whitespace are rejected. Blank
lines and lines beginning with `#` are ignored. `nanokvm-release` is always
installed and pulls the matched kernel, modules, firmware, OpenRC integration
and NanoKVM application packages.

Example request:

```text
# Local additions
zstd
iperf3
smartmontools
```

Validate it on any POSIX host without root, QEMU or an Alpine VM:

```sh
./scripts/build-alpine-personal-image.sh \
  --validate-only \
  --profile stock \
  --base-rootfs alpine-minirootfs-3.24.2-riscv64.tar.gz \
  --base-sha256 0000000000000000000000000000000000000000000000000000000000000000 \
  --boot-fit boot-alpine.sd \
  --boot-sha256 0000000000000000000000000000000000000000000000000000000000000000 \
  --nanokvm-repo https://builder.example/nanokvm/stock/recipes \
  --repo-key nanokvm-packages.rsa.pub \
  --packages-file request.packages \
  --output out/request-001
```

`--validate-only` checks request syntax and canonicalizes the package and
repository lists. It intentionally does not read images or keys.

## Build on 192.168.13.3

Create the VM payload from the tested local inputs without copying a private
signing key:

```sh
./scripts/build-alpine-builder-deploy.py \
  --base-rootfs work/alpine/alpine-minirootfs-3.24.2-riscv64.tar.gz \
  --boot-fit work/alpine/final-vm-good/boot-alpine.sd \
  --nanokvm-repo work/alpine/repo-r4/stock/recipes \
  --tuned-repo work/alpine/repo-c906-qualified \
  --repo-key work/alpine/signing/dgrace-6aaddbb6.rsa.pub \
  --qemu-static work/alpine/tuned-builder-full/rootfs/usr/bin/qemu-riscv64-static
```

The deterministic archive includes only the personal-image scripts, required
recovery build files, pinned base and FIT, signed repositories, public key,
static QEMU runner and a checksum manifest. Its embedded `DEPLOY.md` contains
the exact sample command. The builder rejects a private PEM key.

On the current Ubuntu x86_64 VM install the runtime prerequisites first:

```sh
sudo apt-get install python3 qemu-user-static binfmt-support device-tree-compiler
```

`qemu-user-static` and `binfmt-support` are required for APK maintainer scripts;
the bundled Buildroot `mkimage` invokes the host `dtc` when it creates the
recovery FIT. Run one build as root in a fresh output directory. The public key
filename must match the key name used by the APK signatures. The current
development NanoKVM and C906 repositories use the same signing key. If
production separates those keys, repeat the repo-key option once for each
public key.

```sh
sudo ./scripts/build-alpine-personal-image.sh \
  --profile c906-scalar \
  --base-rootfs /srv/nanokvm/input/alpine-minirootfs-3.24.2-riscv64.tar.gz \
  --base-sha256 <verified-base-sha256> \
  --boot-fit /srv/nanokvm/input/boot-alpine.sd \
  --boot-sha256 <verified-boot-sha256> \
  --tuned-repo https://builder.example/nanokvm/c906-scalar \
  --nanokvm-repo https://builder.example/nanokvm/stock/recipes \
  --repo-key /srv/nanokvm/keys/nanokvm-packages.rsa.pub \
  --packages-file request.packages \
  --output /srv/nanokvm/output/request-001
```

The builder extracts the pinned base into a fresh temporary root and adds
only validated package arguments through APK. A C906 request is resolved in
two phases on a non-riscv64 builder: official Alpine and NanoKVM packages first
run all normal configuration scripts under QEMU, then the signed C906 overlay
replaces the matching payloads with APK scripts disabled. This avoids executing
C906-specific BusyBox under qemu-user while retaining the already completed
same-source package configuration. A native riscv64 builder runs the final
upgrade normally. The final repository order is written into the image before
the update-bundle builder runs. Temporary mounts and the QEMU helper are
removed before packaging.

The output contains the root archive, normal FIT, one-shot recovery FIT and
`SHA256SUMS`, plus:

- `request-manifest.txt`: pinned inputs and profile;
- `requested-packages.txt`: canonical explicit additions;
- `installed-packages.txt`: exact installed package versions;
- `repositories`: exact target repository configuration;
- `apk-world.txt`: the resulting APK world;
- `trusted-keys.sha256`: hashes and names of additional repository keys.
- `recovery-manifest.json`: hashes embedded in the generated recovery FIT.

These files are included in `SHA256SUMS`. The builder rejects a root tar stream
larger than 650 MiB so the fixed 768 MiB F2FS partition retains working
headroom. Installation remains a one-shot recovery flow: it preserves exFAT
`/data`, archives `/etc/kvm` from the old root onto `/data`, formats only p2 as
F2FS, installs the generated root and normal FIT, then restores `/etc/kvm` with
its ownership and modes on F2FS. It also restores `/etc/hosts`, `/etc/ssh` and
`/root/.ssh`, keeping the attended-builder hostname, SSH host identity and
authorized operator keys across the format. There is no A/B slot or timed
rollback.

For a manual install, copy the complete verified bundle to the device and run:

```sh
nanokvm-stage-update /path/to/bundle
nanokvm-stage-update --activate /data/nanokvm-update
reboot
```

The first command only verifies and publishes `/data/nanokvm-update`. A source
already on `/data` is consumed by an atomic directory rename, avoiding a second
write of the large root archive. Sources on another filesystem are copied and
rehashed after the write. The second command replaces `/boot/boot.sd` with the
matching recovery FIT; reboot remains explicit.

## Local full-build proof

The complete C906 request path was exercised in a root WSL2 chroot before VM
deployment. The request added nano, proving Alpine fallback. The final run used
the physically qualified repository and exercised it over HTTP, including its
separate `riscv64` and `noarch` package paths. The generated archive retained
the NanoKVM OpenRC runlevels and contained these representative versions:

- busybox 1.37.0-r32;
- coreutils 9.11-r1;
- zlib 1.3.2-r0 from Alpine main;
- lz4 1.10.0-r2;
- zstd 1.5.7-r3;
- xz 5.8.4-r0 from Alpine main;
- openssl, libcrypto3 and libssl3 at 3.5.8-r1;
- nano 9.2-r0 from Alpine community.

The root archive SHA256 was
91a3d5464fdfc951311eba379d6bcb4386cc6f3b6ae99818054ddbd984113cd3;
the generated recovery FIT SHA256 was
32e13f68bac0a413024550810b2b50cbc390abeeb7868cdc992f162096f822ca.
All bundle hashes verified. The request, installed package set, repositories,
world file and verification output are under
`firmware/alpine/evidence/2026-09-19-personal-image-qualified`.

The HTTP frontend was also exercised end to end from an extracted deployment
archive. It rejected an invalid package name, returned HTTP 201 for the first
qualified C906 request, returned the same build as cached on repetition, served
the recovery FIT, and produced a bundle whose complete `SHA256SUMS` verified.
Raw responses and hashes are under
`firmware/alpine/evidence/2026-09-19-attended-api`.

## Deployed VM and physical attended update

The personal-image service is deployed on Ubuntu 24.04 at `192.168.13.3` as
`nanokvm-builder.service`, listening on `127.0.0.1:18085`. Nginx Proxy Manager
Plus exposes `https://nkos.pesin.pro` on standard HTTPS port 443 with the
existing wildcard certificate. The public A record resolves to the proxy, so
the test NanoKVM needs no `/etc/hosts` override and configures this origin as
`alpine.builderURL`.

The final `c906-scalar` request added `nano` from Alpine community and produced
build `6c4cec8ada101dc83502b801`. All ten response artifacts matched their
declared SHA256; the root contained the enabled `sshd` runlevel, update stager
and NanoKVM server. Repeating the request returned the cached build and an
invalid package expression returned HTTP 400.

The generated bundle was downloaded by the NanoKVM over the TLS proxy and
installed through the one-shot recovery on the physical card. After the
unconditional F2FS format, the existing SSH fingerprint, authorized key,
NanoKVM settings and builder hostname were unchanged. `/` used 207 MiB of the
766 MiB usable F2FS volume, `/data` remained exFAT, all seven NanoKVM/SSH
services were started, the UI and update health endpoints returned HTTP 200,
and the kernel log contained no panic, illegal instruction or F2FS error. The
final artifacts were:

- root archive: `cadf9a1d8e01c800e1ee2067a25506a2d8b57cfda264cd7fd1e35c1553ce6915`;
- normal FIT: `549a61d939e79801e623dfc4621f8dccefb9c2638a969b7f3ffdedaea741298e`;
- recovery FIT: `c3d1291f4ca93b695a730c2ce6c6995071d4b6be8d03a075829db68e98cb3377`.

The VM response, device snapshot and UART evidence are under
`firmware/alpine/evidence/2026-09-19-attended-vm-final`.
