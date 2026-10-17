# NanoKVM OS package updates

NanoKVM OS uses native APK packages in the writable system root. Routine
updates preserve settings, user data and independently installed packages;
flashing a full SD image is a separate operation that replaces the card.

## Updating v2.0-a2 or newer to v3.2.0

Read the [v3.2.0 release notes](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v3.2.0).
Use **Settings → System → Updates** (on a2: **Settings → Updates → Package
updates**), or run as root:

```sh
apk update
apk upgrade
reboot
```

`apk update` refreshes repository indexes; `apk upgrade` installs the available
updates. This release has thirteen core packages; optional add-ons and Alpine dependency
updates are additional.

The kernel release remains **7.2.10-nanokvm-os-r5**. Reboot to load the updated
boot images with corrected SD/SDIO clocks. Completed component transactions refresh affected services through
**OpenRC**; an application update briefly disconnects video/control, and no
separate apply command is required. APK updates preserve the existing bootloader.

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
those packages at their old versions. Replace the core package selections with
repository selections before upgrading:

```sh
apk update
apk add nanokvm-base nanokvm-app nanokvm-release nanokvm-kernel-sg2002 nanokvm-kmod-sg2002 nanokvm-firmware-sg2002
apk upgrade
reboot
```

## Signed indexes and automatic key migration

`nanokvm-keys` installs and owns the public release keys. Its installation moves
the official NanoKVM repository from the legacy URL to:

```text
https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb
```

The v3 index is signed with both RSA and ECDSA P-256 during the transition.
The RSA-signed `APKINDEX.tar.gz` remains published for older installations,
allowing them to obtain the update and keys through their existing repository.
No manual key download or repository edit is required for the official URL.
Custom repositories and C906 overlay entries are left unchanged.

The legacy index contains the thirteen core packages. The v3 index also includes
the optional RustDesk APK; install it from Software after updating the system.

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

**Image v1.4** is the installation bundle, while **v3.2.0** is the current
application release. Component updates keep the original image identity;
About shows both versions. Reflashing is not required for routine updates.
