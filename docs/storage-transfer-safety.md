# Image transfer safety

Image upload/download hardening is adapted from the fixes in
[sipeed/NanoKVM#957](https://github.com/sipeed/NanoKVM/pull/957), especially
[34846418](https://github.com/sipeed/NanoKVM/commit/34846418cc25c69f6917ed51ccd363bef71426b5)
and [631375ae](https://github.com/sipeed/NanoKVM/commit/631375ae4f4f1974299386dc1ea931c0a77c11a9).
Thanks to watermeko and imguoguo for the upstream work.

## Image ownership

Deletion and publication of a replacement image check every LUN of the storage
function and the persistent USB disk markers. They refuse an unreadable state,
case aliases and hard links to an attached image. The check and filesystem
mutation share the HID lock with USB composition changes and local, remote and
network-boot media transitions. Long downloads do not hold that lock; the final
destination is checked again when the completed file is published.

The separate FAT32 user disk retains its own exclusive mount/transfer lock.
This change does not export the raw data partition or add live browsing of a
filesystem owned by the connected computer. Root shell scripts that directly
modify configfs must still coordinate with the application.

## Transfer limits and formats

Uploads, native curl downloads, Go fallback downloads and resumed downloads
reserve 128 MiB of the destination filesystem's available space. A resumed
download needs room for its remaining bytes, not the already stored prefix.
The limit also applies when HTTP does not announce Content-Length. The budget
is a snapshot, not a reservation against unrelated applications writing to the
same filesystem; filesystem write errors still abort the transfer.

Native transfers retain curl/OpenSSL, direct-to-file output without an optional
checksum, and the optimized SHA-256 path when verification is requested.
The curl streaming limit requires curl 8.4 or newer (the Alpine base supplies
a newer version). A browser request has an additional bounded allowance for
multipart headers. Rejected transfers do not publish a destination image.

Browser uploads accept .iso (ISO 9660 or UDF volume recognition descriptors)
and .img (nonempty raw images made of complete 512-byte sectors). Detection is
a format check, not a guarantee that an image boots on a particular computer.

## Restart cleanup

At service startup, cleanup examines only the data directory's immediate
children, removing regular files with the exact reserved numeric names
.nanokvm-upload-N and .nanokvm-download-N. It skips symbolic links,
directories, unrelated similarly named files and a partial download referenced
by valid resume metadata and with a valid partial size. Cleanup completes
before new transfers or automatic resumption start.

A crash can leave a checkpoint for automatic ETag/Range-validated resumption;
a completed failed or cancelled attempt cleans up its temporary file.
The resumable metadata remains runtime-only and does not survive a reboot.
