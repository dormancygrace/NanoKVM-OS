# Upstream fixes adopted on 2026-10-01

This integration starts at NanoKVM OS commit
`68650f4330a280b17b1cdc82fbc8cba2e9ef5577`. It carries the applicable source
fixes found in the Sophgo/Sipeed/Radxa review, with the existing source pins.

## MaixCDK

`0002-system-file-lifetime.patch` backports the system-helper changes from
[Sipeed 04bfdcfe](https://github.com/sipeed/MaixCDK/commit/04bfdcfe7f92ae428b6c0ab3bacf1a06b958bb8e)
and [6369151b](https://github.com/sipeed/MaixCDK/commit/6369151b30e3176f4e1eca6644ed46f40a65ec19)
onto v4.11.3. CPU/NPU clock readers close their stream on a match and at EOF;
OS-version/device-key readers close it after a failed read; an empty runtime
version returns an empty string after destroying the file object. Filesystem
API changes and MaixCAM2 changes from the surrounding commits are excluded.

`apply-maixcdk-fixes.py` checks both affected source files before writing,
preflights patches without fuzz, verifies resulting hashes and accepts repeated
application. The board-service builder requires the reviewed I2C and system
fixes before compilation. Host tests link the actual MaixCDK system, filesystem,
logging and error sources. These helpers are mostly discarded by the current
board-service linker; this fixes the pinned source subset and future users of it.

## AIC8800 SDIO

`linux-7.3-sdio.patch` is the SDIO-only excerpt of
[Radxa eae57ef2](https://github.com/radxa-pkg/aic8800/commit/eae57ef2297ce7b897549173ea787f01f35631df).
It handles cfg80211's changed probe status and cookie APIs: RoC cookies are
stored before firmware submission, management cookies survive until TX
confirmation, and the assigned probe cookie is stored before queuing work.
The changes use version guards; the production release remains on Linux 7.2.6.

The standard source-preparation chain applies this after ownership, firmware,
clock, monitor and survey fixes. Six-file hashes reject unknown or partial
compatibility patches. The survey helper accepts the final combined hash so
the entire chain remains repeatable.

The existing survey patch also needed corrected context: a blank line belonging
to the new function body was previously represented as unchanged context.
The regenerated patch produces exactly the already reviewed SHA-256. Applying
it with `patch --fuzz=0` avoids Git silently skipping files under ignored
`work/` directories.

## Verification and limits

See `evidence/2026-10-01-upstream-fixes/verification.json` for source and artifact
hashes and the completed checks. Hardware qualification is separate. No device
installation, firmware publication or change to the busy runtime was performed.
The existing frozen source-component manifests describe their historical
candidates; they are not manifests for this new build.

No new proven SG200x VI/VPSS/VENC/JPU fix was found. The selected Sophgo osdrv,
MPI and sensor revisions already contain the current upstream changes.
SDK 4.2's AliOS restructuring is a platform migration. Other Sophgo SoCs and
duplicate fixes are excluded.

## CryptoDMA remains unresolved

The historical SPACC hang change switches an infinite poll to interruptible
waiting, but uses a global flag, has no timeout and does not distinguish DMA
errors. It already exists in the selected vendor Linux reference. Our diagnostic
backend already has bounded polling and the three-bit interrupt acknowledgement.
The source review does not establish that the vendor IRQ wait resolves the
system-wide H.265 + AES hang reported in
[Sophgo issue #9](https://github.com/sophgo/sophpi/issues/9).
The experimental AES backend remains excluded from production by default.
