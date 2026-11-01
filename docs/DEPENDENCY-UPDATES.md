# Dependency maintenance

NanoKVM OS uses two complementary update checks: Dependabot creates dependency PRs, and **Upstream dependency watch** checks current pins, available versions and errors in its Actions log and job summary. Separate jobs propose eligible Linux updates and the configured native/platform dependencies as draft PRs. Neither job creates or updates issues. Standard GitHub-hosted runners suffice for this public repository. Neither service deploys to devices or automatically merges changes.

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
| Major `@types/node` bumps | The watcher reports latest; types stay aligned with the selected Node 26 runtime until a deliberate migration. |
| `pion/dtls/v3`, `pion/ice/v4`, `pion/srtp/v3` | The watcher checks upstream tags. Local replacements contain patches; changing `require` alone would not update their implementation. |
| `github.com/rs/cors/wrapper/gin` | The watcher checks the Go module proxy's actual nested-module release. Dependabot previously tried a parent tag that contains no such package. |
| `github.com/mervick/aes-bridge-go` | The watcher checks the Go proxy for the actual module. Upstream tagged `v2.0.0` without the required `/v2` module path, so the valid latest pseudo-version is pinned instead. This dependency is used only by compatibility/auth tests; normal server builds do not import it. |

The last two are intentionally handled as manual update candidates, not silently treated as current. Dependabot does not generate updates for ignored module paths, including security updates; these exceptions therefore still need maintainer security review.

The Go, frontend, script and additional-Go-module CI suites remain mandatory. RustDesk now has a host `cargo test --locked` job so Cargo dependency PRs are actually exercised. Device-only RustDesk tests are marked ignored and do not run in CI.

## Independent upstream checks

`Upstream dependency watch` runs daily at 05:23 UTC and supports manual dispatch. It reads pins from repository files without executing downloaded package code. It reports:

- **Every entry in `platform/sources.lock`**: Linux, Buildroot, U-Boot, OpenSBI, Sophgo SDK/drivers/media/sensors, AIC8800, cryptodev, json-c, miniz, inih, tinyalsa, zlib-ng, xz, Opus, BusyBox devmem source, wireless-regdb, Go, Node, pnpm, Alpine minirootfs and the QEMU build helper.
- **All direct frontend dependencies**, including dev dependencies and npm aliases, against registry `latest`, independently of cooldown or whether Dependabot can produce a lockfile update. Indirect frontend packages remain owned by Dependabot/security alerts. Unrecognized version ranges fail visibly instead of being skipped.
- **Buildroot stable releases only**, including stable point releases. Selected recipes (binutils, musl, initramfs BusyBox, filesystem and host tools) follow that Buildroot release; master snapshots, release candidates and per-recipe development changes are not reported or proposed separately. GCC stable releases retain their independent check.
- **All stock Alpine dependencies named in local APKBUILD `depends`/`makedepends`**, plus optional VPN packages and the core BusyBox and APK tools. Stable main/community availability is checked, for the optional VPN packages too. Installed device versions are not inspected.
- **RustDesk stable protocol reference**, paired with the hbb_common submodule commit from that release (never hbb_common/main).
- Patched Pion versions, the vendor kernel reference, the nested CORS module and AES Bridge Go described above.

**inih and miniz:** monitor published stable GitHub releases, resolving each
release tag to its exact commit. Keep the existing post-release pins until a
stable release contains them: inih `2bbdec4a` includes fixes after r62, and
miniz `2948eeba` includes fixes after 3.1.2. An older release is reported as
`pinned ahead` and does not create a PR or roll back fixes. Divergent release
history produces a review notification without rewriting pins. Branch-tip
commits and prereleases do not qualify as release updates.

**tinyalsa cadence:** monitor commits on the upstream default branch quarterly, at 05:41 UTC on January 1, April 1, July 1 and October 1. Both reports and draft PR discovery skip tinyalsa in daily runs; the quarterly run checks only tinyalsa. Manual dispatch and CLI runs default to all sources; use --cadence daily or --cadence quarterly to select a group. The existing tinyalsa PR remains available for review. Upstream still publishes releases, but the latest is [v2.0.0 (February 8, 2021)](https://github.com/tinyalsa/tinyalsa/releases/tag/v2.0.0), so commit tracking is retained.

**PicoClaw monitoring is disabled:** neither the report nor the PR publisher queries its release channel. On-demand installation and updates through the application's latest-download URL remain unchanged.

**Linux release policy:** the kernel.org feed is filtered to point releases `X.Y.Z` with `Z >= 1`. Initial series releases (both `X.Y` and `X.Y.0`) and release candidates are skipped. For example, `7.3` / `7.3.0` do not replace `7.2.11` as an update candidate, but `7.3.1` does. A series' first release can still be pinned by hand (the current pin is `7.3`, which builds as `7.3.0`); the publisher then proposes `7.3.1` with the release name and kernel version moved together. This filter applies only to Linux; other components retain their own release policies. Kernel candidates also produce draft PRs through the separate kernel publisher described below, rather than native Dependabot.

Errors fail the workflow and remain explicit in the report. Issue publication and its CLI have been removed; the report job has no write permissions. GitHub tag listing is paginated; exceeding its bounded scan is an error rather than an incomplete claim of the latest version. Pinned-ahead results never trigger a downgrade.

## Automatic kernel PRs

The independent kernel job runs on main with the same daily schedule, even if an unrelated upstream check fails. It uses the same Linux version filter and creates a draft PR for each eligible newer version. It updates the archive URL/SHA-256 from kernel.org's HTTPS checksum list, both source manifests, build version/extraction paths, fixed build date, config heading and current README versions. It does not modify main.

A version has a dedicated branch and at most one PR. Later runs leave open or closed PRs and maintainer edits alone; closing a proposal rejects that version. A newer version can create a new proposal. A retry after branch creation can finish opening the missing PR without rewriting the branch.

These are source-update proposals: kernel patches, olddefconfig changes, drivers and device behavior still require review, a build and a device test. The Release build workflow comments on such a PR with the outputs it changes; they must be qualified before merging. No kernel build, signature verification, automatic merge or deployment is claimed.

The publisher jobs have contents/pull-requests write permissions; the report job remains read-only. Repository Actions settings must allow GitHub Actions to create pull requests; default token permissions remain read-only. It uses GITHUB_TOKEN without a personal token. GitHub can require a maintainer to select **Approve workflows to run** on these PRs before CI starts ([GitHub behavior](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow#triggering-a-workflow-from-a-workflow)).

Run `python3 scripts/propose-kernel-update.py` for a read-only preview; `--publish` explicitly enables draft PR creation.

## Native dependencies and integration notifications

The dependency publisher runs daily at 05:23 UTC on main, independently of the report and kernel jobs; tinyalsa uses the quarterly schedule described above. Explicit pull_request policies in .github/upstream-watch.json cover 33 configured sources:

| Sources | Draft PR behavior |
|---|---|
| U-Boot, OpenSBI, Buildroot, zlib-ng, xz | Update source pins; local patches/configuration and output hashes still require qualification. |
| AIC8800, cryptodev, Sophgo osdrv/cvi_mpi/sensors, Sipeed SDK | Update the commit and the exact selected Git tree/blob IDs. **AIC8800 tracks radxa-pkg/aic8800.** Commits with unchanged selected paths do not create a PR. |
| json-c, miniz, inih, Opus, tinyalsa, wireless-regdb, BusyBox devmem | Update source commits or archive URLs and SHA-256, plus companion metadata and archive/extraction paths where applicable. |
| Node.js, pnpm, Alpine minirootfs | Update archive pins; Node stays in the selected major and minirootfs in the selected Alpine branch. |
| Patched Go runtime, GCC, Alpine branch migrations | Open an integration notification: version guards, local patches and system migrations need deliberate work. |
| Patched Pion DTLS/ICE/SRTP, CORS wrapper, AES Bridge | Open an integration notification without pretending that a version bump rebases local code or fixes upstream module paths. |
| RustDesk + hbb_common | One integration notification per stable RustDesk release, recording its exact commit and bundled hbb_common gitlink. No prereleases or standalone hbb_common branch updates; active protocol pins change only after review and testing. |
| Sophgo kernel reference | Open an integration notification for the new reference. |

**Buildroot policy:** only newer stable release archives create PRs, for example 2026.08.1 or 2026.11. RCs, snapshots, master commits and individual recipe diffs are excluded from both reports and PR creation. Selected recipes inherit the Buildroot pin and are reviewed with the release upgrade. The offline inventory still checks that all selected recipes are accounted for; this policy does not disable that coverage check. GCC remains independently monitored for stable releases.

Pin-update PRs hash downloaded archive bytes over HTTPS without extracting or executing them; this does not claim upstream signature verification. Git inputs resolve full commits and selected object IDs. If preparation cannot complete, the bot creates a review-only notification carrying the error, with no partial pin changes, and the job reports failure. Failed upstream discovery remains an error rather than “up to date”; other components continue to be processed.

Every proposal includes a small .github/upstream-candidates/<component>.json record and is a draft. **Review-only PRs do not change an active dependency.** Their record preserves the candidate, links and integration work; merging it acknowledges the notification, not completion of an upgrade. Active pins must be updated and validated separately before calling the dependency upgraded.

Each component/candidate has a deterministic branch, `deps/upstream-<component>-<digest>` (Linux: `deps/upstream-linux-<version>`). The bots recognise a candidate by the branch name after its prefix, so proposals opened under an earlier prefix still count. A rerun preserves open PRs and manual edits; closed proposals are not recreated. Changed candidates may create new PRs. Merged candidate records also suppress repeated notifications. Neither publisher rewrites existing proposal branches, enables auto-merge, installs software, nor changes qualified output hashes.

QEMU and stock Alpine APK updates remain report-only. Dependabot continues owning normal npm/Go/Cargo/Docker/Actions/Rust toolchain updates. Linux keeps its dedicated first-point-release policy.

Run python3 scripts/propose-upstream-updates.py for a read-only preview (including archive hashing). Add --publish to create PRs, and optionally --output /tmp/upstream-candidates.json to save the result summary.

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

Run `python3 tests/test-upstream-watch.py` for offline contracts, or `python3 scripts/check-upstream.py --output report.md` for a live read-only report. The command is read-only: GitHub credentials are used only for API reads, and the report job has only `contents: read` permission. The separate publisher jobs have narrowly scoped write permissions.

Dependency availability does not prove compatibility. Native changes need matching hashes, preservation of local patches, rebuilds and relevant validation. Node major changes, Alpine branch migrations, firmware and module ABI changes remain explicit decisions. Actual package installation verifies APK signatures; the watcher only reads metadata over HTTPS.

References: [Dependabot configuration](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference), [supported ecosystems](https://docs.github.com/en/code-security/reference/supply-chain-security/supported-ecosystems-and-repositories), [default cooldown](https://github.blog/changelog/2026-07-14-dependabot-version-updates-introduce-default-package-cooldown/).
