# Dependency maintenance

NanoKVM OS uses two complementary daily checks: Dependabot creates dependency PRs, and **Upstream dependency watch** checks current pins, available versions and errors in its Actions log and job summary. A separate job proposes eligible Linux updates as draft PRs. Neither job creates or updates issues. Standard GitHub-hosted runners suffice for this public repository. Neither service deploys to devices or automatically merges changes.

## Dependabot

`.github/dependabot.yml` checks these manifests daily at 06:00 UTC:

- All repository GitHub Actions.
- `web/package.json` and `web/pnpm-lock.yaml`, including development and indirect dependencies.
- All five Go modules, including dependencies inside the patched Pion trees.
- `addons/rustdesk/Cargo.toml` and `Cargo.lock`.
- Both Pion Dockerfiles.

Minor/patch updates are grouped; major npm/Go/Cargo upgrades remain separate candidates. Security updates have separate groups and are enabled in repository settings. The explicit **three-day cooldown** delays normal update PRs, not the check's visibility of new releases. Daily checks avoid adding a full week after that waiting period. Open-PR limits limit queued PRs, not inventory coverage.

Intentional exceptions have separate monitoring:

| Dependabot exception | Independent check / reason |
|---|---|
| Major `@types/node` bumps | The watcher reports latest; types stay aligned with the selected Node 24 runtime until a deliberate migration. |
| `pion/dtls/v3`, `pion/ice/v4`, `pion/srtp/v3` | The watcher checks upstream tags. Local replacements contain patches; changing `require` alone would not update their implementation. |
| `github.com/rs/cors/wrapper/gin` | The watcher checks the Go module proxy's actual nested-module release. Dependabot previously tried a parent tag that contains no such package. |
| `github.com/mervick/aes-bridge-go` | The watcher checks the Go proxy for the actual module. Upstream tagged `v2.0.0` without the required `/v2` module path, so the valid latest pseudo-version is pinned instead. This dependency is used only by compatibility/auth tests; normal server builds do not import it. |

The last two are intentionally handled as manual update candidates, not silently treated as current. Dependabot does not generate updates for ignored module paths, including security updates; these exceptions therefore still need maintainer security review.

The Go, frontend, script and additional-Go-module CI suites remain mandatory. RustDesk now has a host `cargo test --locked` job so Cargo dependency PRs are actually exercised. Existing device-only RustDesk tests stay explicitly ignored without a coordinated device; host CI does not claim hardware qualification.

## Independent daily check

`Upstream dependency watch` runs daily at 05:23 UTC and supports manual dispatch. It reads pins from repository files without executing downloaded package code. It reports:

- **Every entry in `platform/sources.lock`**: Linux, Buildroot, U-Boot, OpenSBI, Sophgo SDK/drivers/media/sensors, AIC8800, cryptodev, MaixCDK, json-c, miniz, inih, tinyalsa, Opus, BusyBox devmem source, wireless-regdb, Go, Node, pnpm, Alpine minirootfs and the QEMU build helper.
- **All direct frontend dependencies**, including dev dependencies and npm aliases, against registry `latest`, independently of cooldown or whether Dependabot can produce a lockfile update. Indirect frontend packages remain owned by Dependabot/security alerts. Unrecognized version ranges fail visibly instead of being skipped.
- **Selected Buildroot recipes** against the pinned release and current Buildroot master: compiler selection (GCC/binutils), musl, initramfs BusyBox, filesystem tools and selected host build tools. GCC stable releases also have their own check. Recipe changes are review candidates, not automatically compatible upgrades.
- **All stock Alpine dependencies named in local APKBUILD `depends`/`makedepends`**, plus optional VPN packages and core BusyBox/OpenSSL/APK tools. Stable main/community availability is checked; optional VPN packages may use the configured edge/community fallback. Installed device versions are not inspected.
- **RustDesk protocol/reference sources**, hbb_common and OneKVM; **PicoClaw's latest-release channel** and presence of the required riscv64 archive.
- Patched Pion versions, the vendor kernel reference, the nested CORS module and AES Bridge Go described above.

**Linux release policy:** the kernel.org feed is filtered to point releases `X.Y.Z` with `Z >= 1`. Initial series releases (both `X.Y` and `X.Y.0`) and release candidates are skipped. For example, `7.3` / `7.3.0` do not replace `7.2.11` as an update candidate, but `7.3.1` does. This filter applies only to Linux; other components retain their own release policies. Kernel candidates also produce draft PRs through the separate kernel publisher described below, rather than native Dependabot.

Errors fail the workflow and remain explicit in the report. Issue publication and its CLI have been removed; the report job has no write permissions. GitHub tag listing is paginated; exceeding its bounded scan is an error rather than an incomplete claim of the latest version. Pinned-ahead results never trigger a downgrade.

## Automatic kernel PRs

The independent kernel job runs on main with the same daily schedule, even if an unrelated upstream check fails. It uses the same Linux version filter and creates a draft PR for each eligible newer version. It updates the archive URL/SHA-256 from kernel.org's HTTPS checksum list, both source manifests, build version/extraction paths, fixed build date, config heading and current README versions. It does not modify main.

A version has a dedicated branch and at most one PR. Later runs leave open or closed PRs and maintainer edits alone; closing a proposal rejects that version. A newer version can create a new proposal. A retry after branch creation can finish opening the missing PR without rewriting the branch.

These are source-update proposals: kernel patches, olddefconfig changes, drivers and device behavior still require review and a coordinated build/test. The publisher keeps the qualified output hashes in platform/expected.sha256 unchanged; they must be regenerated from validated outputs before merging. No kernel build, signature verification, automatic merge or deployment is claimed.

The kernel job alone has contents/pull-requests write permissions. Repository Actions settings must allow GitHub Actions to create pull requests; default token permissions remain read-only. It uses GITHUB_TOKEN without a personal token. GitHub can require a maintainer to select **Approve workflows to run** on these PRs before CI starts ([GitHub behavior](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow#triggering-a-workflow-from-a-workflow)).

Run `python3 scripts/propose-kernel-update.py` for a read-only preview; `--publish` explicitly enables draft PR creation. Offline tests are in `scripts/test-kernel-update-pr.py` and run in CI.

## Which BusyBox and QEMU?

The installed operating system uses **stock Alpine BusyBox** and OpenSSL, updated through signed APK repositories. The optional C906 rebuild scripts are retained for research and are not the production baseline.

There are two separate build inputs: Buildroot's BusyBox for the early-boot initramfs, and the standalone BusyBox source used to build only `devmem` for the vendor media library. They do not replace Alpine's BusyBox.

`qemu-riscv64-static` runs riscv64 Alpine programs and package-install scripts on the x86-64 build host. It is not installed on NanoKVM. The watcher checks published versions in the pinned Ubuntu series; a changed Debian/Ubuntu revision is a review candidate, never a lexicographically selected automatic upgrade.

## Preventing coverage drift

`python3 scripts/check-upstream.py --inventory` validates that:

1. Every tracked npm, Go, Cargo and Docker manifest has a Dependabot directory.
2. Every platform source-lock entry has exactly one monitor owner.
3. Every explicitly selected Buildroot package has a monitored recipe or a declared feature relationship.
4. Source-pin expressions resolve unambiguously; frontend dependency syntax is understood.

The same contracts run in CI. Adding a new input without its monitor fails CI. New Python/Ruby/PHP/Maven manifest families also fail visibly until coverage is implemented.

This is comprehensive coverage of the declared inputs above, **not a claim to discover every upstream vulnerability or unversioned binary**. Transitive native recipes are maintained through Buildroot; the watcher does not independently audit every C library's upstream/CVEs. Opaque base FIP and vendor blobs follow their SDK source pins; local patches require review. Historical experiments that are not active manifests/build inputs are not production dependencies. Additions must be reflected in the coverage contracts.

## Validation and integration

Run `python3 scripts/test-upstream-watch.py` for offline contracts, or `python3 scripts/check-upstream.py --output report.md` for a live read-only report. The command is read-only: GitHub credentials are used only for API reads, and the report job has only `contents: read` permission. The separate kernel publisher has narrowly scoped write permissions.

Dependency availability does not prove compatibility. Native changes need matching hashes, preservation of local patches, rebuilds and relevant validation. Node major changes, Alpine branch migrations, firmware and module ABI changes remain explicit decisions. Actual package installation verifies APK signatures; the watcher only reads metadata over HTTPS.

References: [Dependabot configuration](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference), [supported ecosystems](https://docs.github.com/en/code-security/reference/supply-chain-security/supported-ecosystems-and-repositories), [default cooldown](https://github.blog/changelog/2026-07-14-dependabot-version-updates-introduce-default-package-cooldown/).

### Audit on 2026-10-12

- ESLint 10.12.0 is visible independently of the three-day PR cooldown (the previous October 5 run occurred before that waiting period elapsed).
- U-Boot 2026.10 is reported from stable tags; the archive index still advertised 2026.07 during this audit.
- The CORS wrapper was updated to `v0.0.0-20261008204034-5d834403882d` after isolated and integrated validation. The upstream diff changes the wrapper's dependency files, not its Go implementation.
- Follow-up AES migration: the old module moved to `aes-everywhere-legacy`; its successor is the separate `aes-bridge-go` repository. Tests now use its `EncryptLegacy` API at `v0.0.0-20250728011030-7e095cdac2f2` (the latest Go-resolvable commit; Go source matches upstream v2.0.0). The old dependency was removed. Fixed OpenSSL vectors, form/JSON login coverage, malformed-ciphertext rejection and UTF-8/block-boundary compatibility are retained. Production decryption stays in the validated standard-library implementation; this change does not alter the wire format or switch clients to GCM.
