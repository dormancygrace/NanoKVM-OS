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

## Browser-mounted ISO reads

Browser media remains read-only and validates the response ID, exact payload
length and image bounds. The server reads the four-byte ID separately, then
receives the payload into a reusable 64-byte-aligned buffer. The NBD worker
finishes writing that reply before the buffer can be reused. Unsolicited
WebSocket data cannot overwrite a reply owned by NBD.

After two consecutive sequential transitions with requests of at least 64 KiB,
the server reads ahead in two 1 MiB blocks, starting at the first unread byte. This bounds active payload buffers to
approximately 3 MiB per session, including the direct/cross-block output buffer.
The two buffers are retained and reused across later sequential streams.
The final block is shortened at the image boundary. Small isolated reads use
the exact requested length; a seek outside the current window drains its one
outstanding browser response and returns to direct reads.

Read-ahead improves sustained ISO transfers but can add latency to a seek while
a block is already in flight. The current browser protocol cannot cancel that
response without closing the session. A single 15-second NBD read budget covers
both draining the old response and receiving the replacement; expiry closes the
session, staying below the kernel's 20-second NBD timeout. Browser disconnect and access revocation
still close the socket, interrupt pending reads and tear down the NBD device.
These reader changes do not alter Ethernet coalescing or NAPI settings.

Ethernet startup can optionally apply the [SG2002 receive batching policy](ethernet-rx-batching.md).
It is disabled by default and affects the whole physical interface when enabled;
its throughput/latency tradeoff is separate from browser buffering and seeking.
## HTTP(S)-mounted ISO reads

In **Images → From URL**, enter a direct HTTP or HTTPS URL to an immutable ISO
and, optionally, HTTP Basic credentials. Enable the USB virtual disk first and
eject any other image. The server must support byte-range GETs: the device
probes a real 2 KiB range and requires an exact `206 Content-Range` response.
Neither HEAD nor an `Accept-Ranges` header alone is sufficient. Every read
checks the range, total size and body length; compressed responses are rejected.
The existing optical size/alignment limits apply, including the kernel's DVD
capability check. This source presents a read-only CD/DVD, not a writable disk.

The device fetches blocks on demand. It retains at most 1 MiB of image data in
RAM, reusing the same buffer; after two sequential transitions of at least
64 KiB it fills a window up to 1 MiB. There is no image file, persistent cache
or full-image download on the SD card. Network availability and latency remain
part of each uncached read. Each request, including its response body, has a
8-second timeout; disconnect cancels an outstanding request immediately.

Use a stable, unchanging object for the entire mount. A strong ETag is pinned
with `If-Match`; otherwise a valid Last-Modified timestamp is pinned with
`If-Unmodified-Since`. A changed or missing pinned validator or a changed total
size fails the session. Without a usable validator, same-size content changes
cannot be detected. Cached blocks are not revalidated on every access.

HTTPS validates the server certificate. Redirects are bounded, cannot downgrade
HTTPS to HTTP, and do not forward Basic credentials to another origin. URLs
with embedded credentials are rejected; use the separate fields. URL queries
and credentials remain in process memory and are omitted from media status,
source error messages and request-error console diagnostics. HTTP itself does
not encrypt credentials or image data. LAN servers are supported; only an
authenticated administrator can create or disconnect a mount.

The HTTP session belongs to the device and survives closing the page, signing
out, or closing the browser. Disconnect it from **Images** when finished.
Restarting the server/device, disabling USB storage, ejecting the medium, or a
source read failure ends the session. A failed source is detached and its error
appears in media status; there is no automatic reconnection to possibly changed
content. Startup cleanup only ejects a stale NBD backing when both its ownership
lock and kernel PID show that it is unused. Local images, browser ISO, HTTP ISO
and network boot share one exclusive virtual medium.

This URL source supports HTTP(S). SMB, SFTP and remote NBD URLs are not supported.
The source interface separates block reads from the common local NBD/USB
lifecycle, while browser media keeps its own WebSocket buffering and prefetch.

### HTTP failures, trust and redirects

A read makes at most two attempts, each capped at eight seconds, within one
16-second budget below the kernel NBD timeout. Timeouts, interrupted connections
and HTTP 5xx responses are retried once on a fresh connection. Authentication,
missing files, changed validators, invalid ranges and oversized responses fail
with specific errors; an interrupted short body can use the one retry. Slow links must deliver an adaptive block of up to 1 MiB
within one attempt; retries do not remove this per-attempt limit.

The original URL is retained in memory. If a resolved signed target expires
(HTTP 401, 403 or 410), it can be resolved again once within that same budget.
The new target must match the pinned size and ETag or Last-Modified validator.
Without a validator, an expired target requires disconnecting and reconnecting.
Credentials remain limited to the original origin and redirects never send a
Referer or downgrade HTTPS to HTTP.

HTTPS uses the device's system CA trust store. For a private CA, install its
root certificate in the Alpine system trust store before connecting; an
untrusted/self-signed server is rejected. Certificate verification cannot be
disabled here. HTTP Basic credentials sent over plain HTTP are not encrypted;
use HTTPS when authentication is required.
