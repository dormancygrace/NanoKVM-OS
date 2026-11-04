# NanoKVM OS package updates

NanoKVM OS uses native APK packages in the writable system root. Routine
updates preserve settings, user data and independently installed packages;
flashing a full SD image is a separate operation that replaces the card.

## Updating an existing installation

Read the [current release notes](https://github.com/dormancygrace/NanoKVM-OS/releases/latest).
The native update path supports v2.0-a2 and newer.
Use **Settings → System → Updates** (on a2: **Settings → Updates → Package
updates**), or run as root:

```sh
apk update
apk upgrade
reboot
```

`apk update` refreshes repository indexes; `apk upgrade` installs the available
updates. This release has nineteen core packages; optional add-ons and Alpine dependency
updates are additional.

Reboot when requested to activate updated kernel or bootloader files.
Completed component transactions refresh affected services through
**OpenRC**; an application update briefly disconnects video/control, and no
separate apply command is required. Since v3.3.0, `nanokvm-bootloader`
also updates the bootloader on the SD card. It verifies the written file and
keeps `/boot/fip.prev` for recovery; see [bootloader updates](BOOT.md#updating-fipbin).

## Update notices and component versions

**Settings → System → Updates** lists the installed NanoKVM OS packages, the
Alpine release and the running kernel (and the kernel of the next boot, when
an update changed it).

Once a day, and a few minutes after every boot, the device checks the NanoKVM
OS repository for newer versions. The check fetches only the NanoKVM index (a
few KiB), never the Alpine indexes, and installs nothing. When updates are
available, administrators see a dot on the settings button and a notice with
the new versions. **Check for updates daily** on the same page turns the check
off; it is stored as `/etc/kvm/update-check.disabled`.

APK keeps its repository indexes in RAM (a tmpfs on `/var/cache/apk`), so
refreshing them no longer writes to the SD card. After a boot, apk downloads
them again on first use; **Software → Packages** offers the refresh.

## Packages previously installed from local files

Installing local `.apk` files can leave file pins in `/etc/apk/world`, keeping
those packages at their old versions. **Install updates** in Settings takes
NanoKVM packages pinned this way back to the repositories before it upgrades;
other packages pinned this way are not offered there. Explicit version bounds
remain intact: a bound such as `<2` or `~1.0` still offers the upgrades APK can
install within it. From a shell, replace the core package selections with
repository selections before upgrading:

```sh
apk update
apk add nanokvm-base nanokvm-app nanokvm-release nanokvm-kernel nanokvm-kmod nanokvm-firmware
apk upgrade
reboot
```

## Signed indexes and automatic key migration

`nanokvm-keys` installs and owns the public release keys. Installing or
upgrading it moves the official NanoKVM repository from the earlier URLs on
`nkos.pesin.pro` to GitHub Pages:

```text
https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb
```

From v3.4.0, new core packages and both indexes are signed only with ECDSA P-256.
Devices on 3.3.x already trust this key and update normally. Devices that trust
only the old RSA key must first install a transition release (2.5 through 3.3.x)
or install the published EC key and current repository URL manually. The old
RSA public key remains installed for historical packages and rollback.
Custom repositories and C906 overlay entries are left unchanged.

The OS indexes contain core packages only. RustDesk, IPMI and model packages
come from the separately signed add-on index on Pages. Add-ons update
independently of OS releases; RustDesk is not installed in the full SD image.

## Other migration cases

- **Experimental C906 overlay:** follow the [stock Alpine migration instructions](../firmware/alpine/README.md#existing-c906-installations).
  Updating NanoKVM packages alone does not replace the overlay.
- **Coming from a2 with OpenVPN 3:** the supported optional client is now
  OpenVPN 2. Install it from VPN settings before reconnecting; existing profile
  files are retained.
- **Stock firmware or pre-v2 betas:** use the [full SD image](INSTALL.md). The [stock migration installer](stock-install.md) is an experimental development path pending device qualification.
  Legacy `.nkos` packages do not migrate the system to Alpine.
- **Original v2.0-a1 image:** it predates the ready-to-use native update path
  documented here; use the full image for a fresh installation.

## Installing and removing software

Use **Settings → Software → Packages**, or `apk add NAME` / `apk del NAME`.
APK resolves dependencies and verifies signatures. Software → Add-ons manages
RustDesk, PicoClaw and acme.sh. RustDesk comes from the signed NanoKVM repository;
PicoClaw and acme.sh use their upstream release downloads.

New full SD images use the OS release version. A package-only hotfix does not
require a new image; the latest-image link continues to offer the newest
published image. Component updates preserve the original installation metadata
shown in About, including historical image numbers. Reflashing is not required
for routine updates.

## Component versions and support

Starting with v3.3.0, each package keeps its version until its content changes.
`nanokvm-release` identifies the overall release and requires its selected
component versions. Different installed component versions are therefore normal.

The v3.3.0 transition is also published on the previous `nkos.pesin.pro`
repository so existing devices can obtain the automatic switch to Pages.
That repository stays at 3.3.0; subsequent releases are served on Pages.

Releases before 3.3 receive no further fixes. Compatibility for upgrading from
those releases remains in 3.4 and 3.5; removal is planned for 3.6 after an audit.

## Package publication and retention

New GitHub Releases contain OS releases only: corresponding source, checksums,
and, when built, the SD image and stock migration archive. APKs, indexes and
add-on downloads live on GitHub Pages, including RustDesk's corresponding
source and APKBUILD. Add-on version tags reserve package revisions without
creating GitHub Releases.

Pages retains the current stable OS release and two previous stable releases,
only the latest beta, and the current version plus two previous versions of
each add-on. RustDesk corresponding sources and APKBUILDs are exempt from
retention and remain available for installed binaries. Packages shared by
retained releases remain available even when
originally built earlier. The stable and beta repository URLs do not change.
The beta feed selects the newer of the current stable and beta releases;
switching back to stable still uses the stable index and permits downgrades.
There is no general historical rollback selector.

The Pages catalog (`package-store/catalog.json`) and its hash-checked files
are the durable input to the next deployment. Completed production jobs stage
`pages-candidate-*` Actions artifacts (90 days) as transport. The Pages workflow
consumes all outstanding candidates, including those from a completed run
whose later step failed. PR and dry-run artifacts are never candidates. An OS
candidate is stored while its release is a draft, but enters an update index
only after the public OS release is published. At most one pending draft per
channel is retained in addition to the published releases.

Deployments are serialized, and losing a pending workflow dispatch does not
lose a candidate: the next assembly scans all outstanding artifacts. A missing
catalog, corrupted object, version reused from a different source commit,
or site larger than 950 MB stops publication and leaves the existing site intact. Resolve a
failed Pages deployment within the artifact retention period. For a rejected
rebuild of an existing version, remove the conflicting transport artifact;
never replace the already deployed version's bytes. Publish a new version.
Candidates and catalog snapshots record their full source commit. A signing
retry can reuse the original candidate only for the same commit; rebuilding
the same version from another commit is refused before public release upload.
The complete public draft is uploaded and verified before its package candidate
is uploaded. An interrupted draft upload can therefore be retried with the
matching new assembly. Once the candidate exists, retries only verify and
publish the original downloads; they never mix newly signed files into that pair.

### First migration from release assets

After merging the publication change, run **APK repository** on `main` with
`bootstrap=true`. This imports the retained OS and add-on versions, their
complete package sets and RustDesk source archives from the existing Releases.
It does not delete or change those releases. Subsequent deployments use Pages
as their source and fail if the catalog is missing; bootstrap is never an
implicit fallback.

Verify the deployed catalog, signed stable/beta/add-on indexes and their APKs
before separately removing legacy technical/add-on releases. Existing RustDesk
Releases remain as source-only archives so already installed APK source links
continue working; their APKs are removed. Keep the add-on
Git tags, which prevent reusing package revisions. Corresponding sources are also archived permanently under
`package-store/sources/<tag>/`, including versions older than the binary
retention window.
Migration and cleanup are separate operator actions; ordinary publication
does not delete existing GitHub Releases or change a running device.

To review the one-time cleanup, run `python3 scripts/cleanup-release-packages.py`.
After verification, `--apply` checks every retained Pages object and verifies
all three deployed indexes with apk-tools 3 before deleting legacy technical
and add-on releases (except legacy RustDesk source archives). It also removes APKs and package metadata from older
all-in-one OS releases and filters their SHA256SUMS, preserving every public
OS release, image, source archive and migration download. Git tags remain.
The script needs authenticated `gh`, `GH_TOKEN` for API reads, and host `apk`.
Cleanup is never run automatically by CI or by a normal Pages deployment.
