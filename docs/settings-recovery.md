# Settings backup, migration and factory reset

**Settings → System → Backup & reset** is available to administrators. Export,
preview and apply require the current administrator password. Restore/reset also
require the matching `nanokvm-base` boot integration; update all OS packages first.

## Export and restore

Download a `.nkos` archive and keep it off the device. Choose a backup password
of 12–256 UTF-8 bytes. The archive contains credentials, private keys and root’s
password hash: it is encrypted with AES-256-GCM, using a random salt and nonce and
PBKDF2-HMAC-SHA256 (600,000 iterations). There is no password recovery.

The archive includes managed NanoKVM settings, web accounts, root’s authorized
SSH keys and password hash, network profiles, supported add-on configuration,
certificates and an informational list of APK world roots. It does **not** include
OS files, app binaries, APK databases, ISOs, user-disk contents, PicoClaw workspace
files or browser-local preferences. Nonstandard paths configured outside the
managed locations and arbitrary third-party APK configuration are excluded.
Archives are bounded: 2 MiB of file contents, 512 KiB per file, 1,024 files and
4 MiB encrypted. Oversize or unsafe entries fail explicitly; they are not silently
omitted. Symlinks are refused, except the standard zoneinfo-backed localtime link.

Choose one restore mode:

- **Migrate to another device** is the default. It preserves the destination’s
  users, credentials, hostname/MAC, networking, VPN identities, certificates,
  USB and monitor settings. Identity-bearing add-on configurations are excluded,
  and so are notification channels (they hold tokens and passwords) and the
  addresses each user has logged in from.
- **Restore this device** replaces the managed settings, including network and
  access credentials. The detected board profile must match. This mode can change
  the address, SSH host keys and login; have physical/local access available.

Restore only to the same or a newer application version within the same major
series. Review the exact file paths, skipped settings and package inventory
before confirming. Binaries are not restored or installed automatically: install
missing packages from Software. Sessions are invalidated on restore/reset.

## Factory reset settings

Reset returns the managed settings to the installed version’s defaults. It does
not downgrade the OS, rewrite boot/kernel binaries, erase disks or remove personal
files. Hardware detection and bootloader/recovery overrides are retained.

| Choice | Kept | Reset |
| --- | --- | --- |
| **Keep users** | Web accounts and passwords, root password, authorized SSH keys, SSH host keys and enabled/disabled SSH state | Other managed OS settings |
| **Keep apps** | **All installed software, including APK packages**, PicoClaw and acme.sh | Managed settings of RustDesk, PicoClaw, acme.sh, VPNs and NanoKVM OS |

The choices are independent and checked initially. **Keep apps never means keep
app settings.** Custom configurations of unrelated APK software remain: deleting
unknown files under `/etc` could break the system or destroy personal files.

Without Keep users, web access returns to `admin` / `admin` and requires a password
change. Root password access is locked, authorized keys are removed and SSH is
disabled. Without Keep apps, optional APK world roots and their unused dependencies
are removed with APK; the `nanokvm-release` system dependency closure is retained.
PicoClaw and acme.sh program files are also removed. Package removal is offline;
if APK cannot resolve it, the operation reports failure instead of forcing it.

Network settings reset regardless of these choices. Reconnect using Ethernet and
DHCP; configure Wi-Fi/VPN again. USB composition returns to the current defaults.

## Applying and recovering an interrupted operation

The preview is bound to the selected options, archive and current settings. If
those change, request a new preview. Applying writes a private bounded journal
under `/var/lib/nanokvm-recovery`, freezes UI configuration/package changes, and
reboots. The boot worker runs before board identity, networking, USB and apps.
APK commit hooks do not restart services while recovery is pending.

A power loss leaves the journal for idempotent replay at the next boot. An ordinary
file error restores the old managed settings; optional packages already removed
are **not reinstalled**. The result is shown on the Backup & reset page. The journal
and its temporary copies of credentials are removed after completion/rollback.
If rollback itself fails, the application remains stopped rather than using
partially replaced access settings. Repair storage via the physical console or
recovery image before retrying; `nkos-update recovery-resume` is an early-boot
operation and refuses to run while NanoKVM-Server is active.

This is a settings-recovery feature, not a complete filesystem snapshot or secure
erasure of a card. Exported archives and a physical recovery path remain important.

An error in a fully decoded, unstarted plan cancels recovery before any settings
are changed: a failure status and one private `failed.json` replace the pending
operation, and normal boot continues. Corrupt or already started journals stay
pending because their state cannot safely be discarded. Networking still starts
for remote repair; existing SSH enablement and authentication policies are not
relaxed. SSH access requires working network settings and credentials. A partial
rollback is durably marked and subsequent boots retry rollback, not the original
reset/restore. Inspect the journal via SSH where available; otherwise use UART.
