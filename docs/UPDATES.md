# NanoKVM OS package updates

NanoKVM OS uses native APK packages in the writable system root. Routine
updates preserve settings, user data and independently installed packages;
flashing a full SD image is a separate operation that replaces the card.

## Updating v2.0-a2 or newer to v2.5-a1

**v2.5-a1 is an alpha release.** Read the [release notes](RELEASE-v2.5-a1.md).
Use **Settings → System → Updates** (on a2: **Settings → Updates → Package
updates**), or run as root:

```sh
apk update
apk upgrade
reboot
```

`apk update` refreshes repository indexes; `apk upgrade` installs the available
updates. The seven core release packages total 36.5 MiB; optional add-ons and
Alpine dependency updates are additional.

The kernel and matching modules update to **7.2.9-nanokvm-os-r1**. Reboot to load
them. Completed component transactions refresh affected services through
**OpenRC**; an application update briefly disconnects video/control, and no
separate apply command is required. APK updates preserve the existing bootloader.

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

The legacy index contains the seven core packages. The v3 index also includes
the optional RustDesk APK; install it from Software after updating the system.

## Other migration cases

- **Experimental C906 overlay:** follow the [stock Alpine migration instructions](../firmware/alpine/README.md#existing-c906-installations).
  Updating NanoKVM packages alone does not replace the overlay.
- **Coming from a2 with OpenVPN 3:** the supported optional client is now
  OpenVPN 2. Install it from VPN settings before reconnecting; existing profile
  files are retained. See the [b1 migration notes](RELEASE-v2.0-b1.md).
- **Stock firmware or pre-v2 betas:** use the [full SD image](INSTALL.md).
  Legacy `.nkos` packages do not migrate the system to Alpine.
- **Original v2.0-a1 image:** it predates the ready-to-use native update path
  documented here; use the full image for a fresh installation.

## Installing and removing software

Use **Settings → Software → Packages**, or `apk add NAME` / `apk del NAME`.
APK resolves dependencies and verifies signatures. Software → Add-ons manages
RustDesk and PicoClaw; PicoClaw uses its official upstream release download
rather than an APK package.

**Image v1.0-a1** is the installation bundle, while **v2.5-a1** is the current
application release. Component updates keep the original image identity;
About shows both versions. Reflashing is not required for routine updates.
