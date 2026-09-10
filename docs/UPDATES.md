# NanoKVM OS application updates

Installing from original firmware? Start with the [full SD image instructions](INSTALL.md); a `.nkos` package alone is insufficient.

The `.nkos` format is independent of original NanoKVM tarball updates. The fixed source is `dormancygrace/NanoKVM-OS` on GitHub; the release asset must be named `NanoKVM-OS-application.nkos`. Beta installations consider prereleases. Discovery runs about three minutes after startup and every 24 hours thereafter. Checking is automatic; downloading and installing require administrator actions.

## Package trust

`NKOSAPP1` magic, a big-endian 32-bit manifest length, raw JSON manifest, a 64-byte Ed25519 signature, and a gzip tar payload form the package. The signature covers `NanoKVM OS application update v1\n` followed by the raw manifest. The manifest binds product, format, architecture, application version, monotonic sequence, native-library fingerprint, payload size/hash, and every file's path/size/hash/mode.

Signature verification precedes extraction. Only regular `NanoKVM-Server` and `web/...` files are allowed. Paths escaping that tree, duplicate entries, links, device nodes, unexpected permissions, wrong architecture, foreign keys and altered content are rejected. Limits are 96 MiB per package, 192 MiB expanded, 64 MiB per file and 4096 files. A renamed legacy tarball still lacks the required signature and format.

The public key is embedded in `server/osupdate/release-ed25519.pub.pem`. The private key is held outside this repository and never shipped to devices. This protects the application update path; it does not prevent a device owner with root access or the Boot flashing interface from replacing the OS.

## Installation and recovery

Uploads and downloads are staged on the SD filesystem at `/kvmapp/.os-update`, not in `/tmp` RAM. An independent `/usr/sbin/nkos-update` process holds the update lock. It validates the package again, checks linkage, stages server/web, and hardlinks the unchanged native libraries. Settings remain outside the replaced application directory.

The helper journals the transaction, stops the application, renames directories, starts the new application and requires five consecutive authenticated loopback health responses. It commits version/sequence metadata only after that succeeds. A failed startup restores the previous application. One previous application tree is retained as the rollback slot.

`S94nanokvm-update` runs before `S95nanokvm` at boot and restores an uncommitted transaction. Failure to recover blocks application startup. Directory/file synchronization reduces exposure to interruption, but sudden power-cut recovery has not been physically qualified. HTTP health proves application startup, not working video or HDMI capture.

The helper and startup recovery scripts belong to the system image. An application package cannot update them, the kernel, rootfs, native libraries or trust root. A firmware image containing this updater is a prerequisite; existing development devices need its one-time system integration. A different native-library fingerprint requires a compatible complete image through Boot flashing.

## Making a release

From `server/`, using the project's selected Go runtime on the build host:

```sh
go run ./cmd/nkos-package \
  --app /absolute/stage/server \
  --key /secure/location/release-ed25519.pem \
  --version 1.0.0-beta.1 --sequence 3 \
  --output /new/output/NanoKVM-OS-application.nkos
```

The sequence above is an example: maintainers must allocate a strictly newer global sequence. The staging directory contains `NanoKVM-Server`, `web/` and the matching `dl_lib/`; only the first two are packed. Signing verifies that the private key matches the embedded public key. The packager verifies and extracts its output before returning success.

Publish the asset on an appropriate GitHub release only after qualification. No GitHub access token is stored in the firmware. Private-repository checks report that the source is unavailable; manual signed upload remains usable. See the [GitHub Releases API](https://docs.github.com/en/rest/releases/releases).

Successful installation is announced in the interface that initiated or observed it. Reloading the interface clears the success notice and Reload button. The installed version remains visible, and the persistent installation result is retained for diagnostics; failure and rollback messages are not hidden.
