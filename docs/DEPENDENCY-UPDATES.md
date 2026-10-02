# Dependency maintenance

The public repository uses Dependabot for supported manifests and a weekly upstream watch for native sources and patched forks. Both use standard GitHub-hosted runners. No paid service, personal access token, VM or new artifact storage is required. Security updates remain enabled in repository settings.

## Automatic dependency pull requests

`.github/dependabot.yml` checks weekly on Monday:

- GitHub Actions used by the repository workflows;
- `web/package.json` and `web/pnpm-lock.yaml`, including development and indirect dependencies;
- all six Go module directories, including dependencies of the three patched Pion trees;
- the two Dockerfiles used by Pion examples/tests.

Minor and patch updates are grouped by ecosystem; major Go/npm upgrades remain separate PRs. Security updates have their own groups and are not tied to the weekly version-update schedule. Limits cap the number of open version PRs, not the set of eligible versions. Nothing enables automatic merging or device deployment.

The three locally replaced Pion modules are deliberately excluded from manifest-only bumps. Dependabot cannot rebase our modified source trees. Their upstream tags are tracked in the native dashboard, while ordinary dependencies inside those trees remain eligible for Dependabot PRs. Changing only the `require` version would not install the corresponding upstream implementation.

Existing server/web CI remains in place. Additional Go modules now have their own build/test matrix; the native watcher has offline contract tests in the script CI job.

The first full DTLS run on unchanged fork sources exposed `TestHandshaker` verification/timeouts and `TestHandshakeMessageCertificate` expecting a pre-Go-1.27 certificate layout (`RawSignatureAlgorithm` is now populated). DTLS compilation is mandatory, but its full test suite is temporarily advisory with an explicit warning and job summary; it must not be represented as passing. Other additional module tests remain mandatory. Review DTLS logs when accepting dependency changes and remove this exception after the baseline is repaired. Evidence: [initial full-suite run](https://github.com/dormancygrace/NanoKVM-OS/actions/runs/37044123822/job/110961182604).

## Native sources and patched forks

`Upstream dependency watch` runs every Monday at 05:23 UTC and supports manual dispatch. `scripts/check-upstream.py` reads current pins directly from the files listed in `.github/upstream-watch.json`, checks public upstream metadata, and maintains one bot-owned GitHub issue named **Upstream dependency update dashboard**. Unchanged reports produce no issue edits or comments. Failed checks remain visible and fail the workflow; they are never reported as current.

The initial catalog covers 29 entries: Linux, Buildroot, U-Boot, platform drivers/SDK sources, CVI MPI, sensors, MaixCDK, json-c, miniz, inih, Go runtime, OpenSSL, Alpine stable branch, Opus, tinyalsa, AIC firmware, vendor libc, Asio, Superfile, OpenVPN 3, codec firmware, apk-tools, and the three patched Pion modules. The selected platform pins come from `platform/sources.lock`; older duplicate Linux/Buildroot entries in `firmware/sources.json` are not treated as the current platform.

Version checks use stable numeric releases; commit checks compare the selected vendor branch (or explicitly reported repository default branch) against the pinned revision. New vendor commits can concern other boards. A dashboard entry is a candidate for review, not proof that an update is compatible. Tag-based checks examine the first 100 tags returned by the upstream API; errors, divergent history and a pin ahead of the queried upstream need maintainer review.

Native changes require updating related hashes, preserving local patches, rebuilding and validating platform outputs, and testing affected hardware paths. The watcher deliberately does not rewrite lock files, archive hashes, firmware binaries or `platform/expected.sha256`.

## Coverage limits

- Dependabot supports specific manifest formats, not arbitrary shell variables or patches. Its documented pnpm support currently lists versions through 10, while our build uses newer pnpm and a version-9 lockfile. Check the first updater job and any lockfile changes; a parsing/resolution failure must be addressed rather than assuming frontend updates work.
- Buildroot updates cover its upstream recipe collection; the dashboard does not independently audit every transitive C library or locally overridden Buildroot package for new versions/CVEs.
- Alpine branch updates are reported. Installed APKs still update through APK, and our package rebuilds/releases are a separate process. A new branch is not automatically substituted into device repositories.
- Base FIP/OpenSBI blobs and local hardware patches have no generic version updater. They require provenance and hardware qualification.
- This configuration does not turn archived experiments or upstream example workflows into production firmware dependencies.

Run `python3 scripts/check-upstream.py --inventory` to validate local pins without networking, `python3 scripts/test-upstream-watch.py` for offline tests, or `python3 scripts/check-upstream.py --output report.md` for a read-only live report. `--publish` requires a GitHub Actions token with `issues: write`; the default invocation never writes remotely.

References: [Dependabot configuration](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference), [supported ecosystems](https://docs.github.com/en/code-security/reference/supply-chain-security/supported-ecosystems-and-repositories), [GitHub Actions billing](https://docs.github.com/en/billing/concepts/product-billing/github-actions).
