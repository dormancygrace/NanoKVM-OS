# Dependency maintenance

The public repository uses Dependabot for supported manifests and a weekly upstream watch for native sources and patched forks. Both use standard GitHub-hosted runners. No paid service, personal access token, VM or new artifact storage is required. Security updates remain enabled in repository settings.

## Automatic dependency pull requests

`.github/dependabot.yml` checks weekly on Monday:

- GitHub Actions used by the repository workflows;
- `web/package.json` and `web/pnpm-lock.yaml`, including development and indirect dependencies;
- all five Go module directories, including dependencies of the three patched Pion trees;
- the two Dockerfiles used by Pion examples/tests.

Minor and patch updates are grouped by ecosystem; major Go/npm upgrades remain separate PRs. Security updates have their own groups and are not tied to the weekly version-update schedule. Limits cap the number of open version PRs, not the set of eligible versions. Nothing enables automatic merging or device deployment.

The three locally replaced Pion modules are deliberately excluded from manifest-only bumps. Dependabot cannot rebase our modified source trees. Their upstream tags are tracked in the native dashboard, while ordinary dependencies inside those trees remain eligible for Dependabot PRs. Changing only the `require` version would not install the corresponding upstream implementation.

Existing server/web CI remains in place. Additional Go modules now have their own build/test matrix; the native watcher has offline contract tests in the script CI job.

All additional Go module suites are mandatory. DTLS test fixtures were repaired for Go 1.27: the handshake harness now creates separate mutable cipher-suite instances for each endpoint, the certificate fixture includes the new DER signature-algorithm field when available, and the entropy-failure test explicitly opts into the custom-reader test path. ICE port-exhaustion tests retain and close every socket so GC cannot free ports before the assertion. No production cryptographic checks are bypassed.

## Native sources and patched forks

`Upstream dependency watch` runs every Monday at 05:23 UTC and supports manual dispatch. `scripts/check-upstream.py` reads current pins directly from the files listed in `.github/upstream-watch.json`, checks public upstream metadata, and maintains one bot-owned GitHub issue named **Upstream dependency update dashboard**. Unchanged reports produce no issue edits or comments. Failed checks remain visible and fail the workflow; they are never reported as current.

The initial catalog covers 24 entries: Linux, Buildroot, U-Boot, platform drivers/SDK sources, CVI MPI, sensors, MaixCDK, json-c, miniz, inih, Go runtime, Alpine OpenSSL/C906 baseline, Alpine stable branch, Opus, tinyalsa, AIC firmware, codec firmware, and the three patched Pion modules. The selected platform pins come from `platform/sources.lock`; older duplicate Linux/Buildroot entries in `firmware/sources.json` are not treated as the current platform.

Version checks use stable numeric releases; commit checks compare the selected vendor branch (or explicitly reported repository default branch) against the pinned revision. New vendor commits can concern other boards. A dashboard entry is a candidate for review, not proof that an update is compatible. Tag-based checks examine the first 100 tags returned by the upstream API; errors, divergent history and a pin ahead of the queried upstream need maintainer review.

Native changes require updating related hashes, preserving local patches, rebuilding and validating platform outputs, and testing affected hardware paths. The watcher deliberately does not rewrite lock files, archive hashes, firmware binaries or `platform/expected.sha256`.

## Alpine OpenSSL and the C906 rebuild

The system uses Alpine `openssl`, `libssl3` and `libcrypto3`, with a C906 rebuild of the Alpine recipe in the default overlay. The historical Buildroot OpenSSL 4 patch is not the current system baseline and is excluded from this check.

The OpenSSL watcher reads `APORTS_COMMIT` from `scripts/build-alpine-tuned-packages.sh`, fetches the pinned `main/openssl/APKBUILD` as text (never executes it), and compares its original `pkgver`/`pkgrel` with all three packages in the official stable `riscv64` APK index. The Alpine branch comes from `scripts/build-alpine-packages.sh`. A newer Alpine version or revision is reported as **C906 rebuild required**. The local revision increment does not hide upstream fixes: Alpine `3.5.8-r1` requires a rebuild even if our old `3.5.8-r0` recipe produced a locally named `3.5.8-r1`.

The report distinguishes the Alpine recipe baseline, the C906 version derived by the build script, and the published Alpine version. It does not claim to inspect installed packages or the published C906 overlay. Updating the aports pin, rebuilding, choosing an appropriate package revision, testing and publishing the overlay remain separate actions. This check supports stable numeric OpenSSL versions and numeric APK revisions; unfamiliar version syntax, missing subpackages and fetch failures are explicit errors. APK index signatures are not verified by this metadata-only HTTPS monitor; actual installation remains subject to APK signature verification.

## Coverage limits

Historical Buildroot OpenVPN 3/Asio, Superfile, private apk-tools, nkos-addons and the unused named vendor-libc loader recipes were removed together with the old rootfs profiles. The Alpine release explicitly conflicts with `openvpn3` and uses stock Alpine OpenVPN when installed. Buildroot now has a dedicated platform-only profile; Alpine recipes define installed system and optional package versions.

- Dependabot supports specific manifest formats, not arbitrary shell variables or patches. The first frontend updater produced PR #26 with a version-9 lockfile that passed frozen installation, build, lint and tests under our pnpm 12 CI. Future updater and lockfile failures still require review.
- Buildroot updates cover its upstream recipe collection; the dashboard does not independently audit every transitive C library or locally overridden Buildroot package for new versions/CVEs.
- Alpine branch updates are reported. Installed APKs still update through APK, and our package rebuilds/releases are a separate process. A new branch is not automatically substituted into device repositories.
- Base FIP/OpenSBI blobs and local hardware patches have no generic version updater. They require provenance and hardware qualification.
- This configuration does not turn archived experiments or upstream example workflows into production firmware dependencies.

Run `python3 scripts/check-upstream.py --inventory` to validate local pins without networking, `python3 scripts/test-upstream-watch.py` for offline tests, or `python3 scripts/check-upstream.py --output report.md` for a read-only live report. `--publish` requires a GitHub Actions token with `issues: write`; the default invocation never writes remotely.

References: [Dependabot configuration](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference), [supported ecosystems](https://docs.github.com/en/code-security/reference/supply-chain-security/supported-ecosystems-and-repositories), [GitHub Actions billing](https://docs.github.com/en/billing/concepts/product-billing/github-actions).
