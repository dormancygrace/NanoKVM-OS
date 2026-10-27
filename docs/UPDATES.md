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
updates. This release has fourteen core packages; optional add-ons and Alpine dependency
updates are additional.

Reboot when requested to activate updated kernel or bootloader files.
Completed component transactions refresh affected services through
**OpenRC**; an application update briefly disconnects video/control, and no
separate apply command is required. Since v3.3.0, `nanokvm-bootloader-sg2002`
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
other packages pinned this way are not offered there. From a shell, replace the
core package selections with repository selections before upgrading:

```sh
apk update
apk add nanokvm-base nanokvm-app nanokvm-release nanokvm-kernel-sg2002 nanokvm-kmod-sg2002 nanokvm-firmware-sg2002
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

The legacy index includes the fourteen core packages. The v3 index also
includes RustDesk, whose APK v3 format cannot appear in a legacy index.
RustDesk remains a separate add-on; install it from Software after updating
the system. It is not installed in the full SD image.

## Other migration cases

- **Experimental C906 overlay:** follow the [stock Alpine migration instructions](../firmware/alpine/README.md#existing-c906-installations).
  Updating NanoKVM packages alone does not replace the overlay.
- **Coming from a2 with OpenVPN 3:** the supported optional client is now
  OpenVPN 2. Install it from VPN settings before reconnecting; existing profile
  files are retained.
- **Stock firmware or pre-v2 betas:** use the [full SD image](INSTALL.md).
  Legacy `.nkos` packages do not migrate the system to Alpine.
- **Original v2.0-a1 image:** it predates the ready-to-use native update path
  documented here; use the full image for a fresh installation.

## Installing and removing software

Use **Settings → Software → Packages**, or `apk add NAME` / `apk del NAME`.
APK resolves dependencies and verifies signatures. Software → Add-ons manages
RustDesk, PicoClaw and acme.sh. RustDesk comes from the signed NanoKVM repository;
PicoClaw and acme.sh use their upstream release downloads.

Full SD images and application releases have independent versions.
Component updates keep the original image identity;
About shows both versions. Reflashing is not required for routine updates.

## Component versions and support

Starting with v3.3.0, each package keeps its version until its content changes.
`nanokvm-release` identifies the overall release and requires its selected
component versions. Different installed component versions are therefore normal.

The v3.3.0 transition is also published on the previous `nkos.pesin.pro`
repository so existing devices can obtain the automatic switch to Pages.
That repository stays at 3.3.0; subsequent releases are served on Pages.

Releases before 3.3 receive no further fixes. Compatibility for upgrading from
those releases remains in 3.4 and 3.5; removal is planned for 3.6 after an audit.
