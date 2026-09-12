# Beta-4 validation and known issues

This summary covers checks on the SG2002 NanoKVM PCIe with an LT6911UXC HDMI receiver. Cube and other board revisions are intended targets, not independently qualified hardware. Short checks, synthetic receivers and browser playback measure different things.

## Beta-4 device check, 2026-09-12

The actual release ext4 rootfs was written to the device from a RAM installer and the complete 1.5 GiB partition hash matched before restoring access settings. The release FIT booted Linux 7.2.5-nanokvm-os and all matched modules. Application sequence 9, the corrected updater, Wi-Fi, WireGuard, storage, CPU/temperature telemetry and USB UAC1 were verified. The existing partition layout and user data partition were retained; this is not a fresh-card partition-creation test.

Chrome displayed QHD H.265 Direct and H.264 WebRTC. Two simultaneous audio receivers each received 860 Opus packets without gaps or timestamp errors; decoding confirmed both channels of a generated stereo tone. Host race tests passed for updater, Dashboard, remote media and audio. The image has an empty root home and no tested private configuration markers. Fresh-rootfs testing found a missing DHCP-to-openresolv hook; this was fixed and the image rebuilt. The corrected release rootfs was installed a second time; its entire partition hash matched, the RAM installer completed and rebooted automatically. DNS and NTP synchronization then worked without manual intervention. QHD H.265 Direct reconnected at 30 FPS, CPU maximum was 1000 MHz and SoC temperature was about 44 C.

The temporary RAM installation fixture encountered a busy data mount after successful write/readback; a retry unmounted it and booted normally. This fixture is not shipped in the release image. Kernel-package boot recovery was also corrected to mount /boot before verification, independently of the later filesystem init script.


## Beta-3 changes

A user-installed signed system package upgraded an existing beta-2 device with the new updater installed for the test. The device rebooted, confirmed the transaction and retained settings. This does not mean the stock beta-2 updater accepts system packages; the public upgrade path is the full beta-3 image.

Current application/backend and browser checks cover Dashboard statistics, WireGuard profile naming, video Apply/Discard controls, automatic codec selection, session counts, Date & Time, recording and navigation. The receiver cache fix restored QHD Direct output after incorrect repeated HDMI detection. Updater/telemetry unit tests and production builds pass. Final full-image structure, file hashes and matched kernel/modules are checked during assembly; fresh-card boot and power-loss recovery have not been physically tested.

## Beta-2 changes

WireGuard profile import, saved routing choice, route creation/removal and browser controls were tested on the device. OpenVPN 3 Core connected through `ovpn` DCO to a local test server, exchanged traffic and restored DNS/routes on disconnect. kTLS and AF_ALG are disabled; OpenSSL retains `/dev/crypto`. These tests do not establish arbitrary provider compatibility or fresh-card first boot.

The final beta-2 kernel #5 and OpenSSL without kTLS were installed and booted on the existing device. WireGuard reconnected automatically and the capture counter returned to 30 FPS in QHD. This is component deployment verification; the complete image has not been flashed onto a fresh SD card.

## What has been checked

| Area | Evidence and practical limit |
|---|---|
| Release assembly | FAT/ext4 structure, kernel/module correspondence, ZIP CRC, unpacked SHA-256, staged server/native/web files and four EDID profiles checked. The complete image has not been booted from a fresh card. |
| Application update | Signed sequence-3 application installed on the device; browser access and unique HTTPS certificate creation/reuse/0600 permissions checked. This does not test full-image flashing or power-loss recovery. |
| Video | H.264/H.265 Direct and WebRTC exercised in Chrome. FHD comparisons request **60 FPS**; QHD is capped at **30 FPS**. Encoder output and network frames are not browser presentation FPS or end-to-end latency. |
| HDMI recovery | Three capture-OFF/restart/enable cycles and one active-video restart passed with H.264 QHD in Chrome after the receiver-reset startup fix. This does not establish recovery from a whole-SoC hang. |
| CryptoDMA | All 14 standard `/dev/crypto` correctness cases matched software, with hardware completion counters proving DMA work. The extended diagnostic API passed 24 short algorithm/mode checks. Neither is an endurance qualification. |
| Kernel comparison | Matched 1024-operation, 16 KiB AES-192-CTR batches with video services stopped measured 48.11 / 48.13 / 48.85 MiB/s for 7.2.4 / 7.2.3 / 7.2.4. The earlier short-batch slowdown did not reproduce; universal speedup is not claimed. |
| T-Head instructions | Shipped ELF disassembly and isolated execution probes verified supported instructions. Flags alone are not proof of emitted instructions. Legacy XTheadVector is distinct from RVV 1.0; closed ISP objects were relinked, not rebuilt. |
| WebRTC PMTU | Chrome IPv4 LAN probing, silent-loss fallback and different budgets for two viewers checked. IPv6, TURN first-leg handling and ICE migration also have isolated transport tests; equivalent browser/tunnel/endurance coverage is incomplete. See [design and limits](webrtc-pmtu-design.md). |
| Wi-Fi | Exact SDIO alias selection and AIC8801 module reload/reconnect with Chrome recovery checked. RTL8733BS builds and is included, but association/reconnect on physical Realtek hardware remains untested. |
| OpenVPN DCO | OpenVPN 3 Core: isolated on-device tunnel, browser start/stop, traffic and DNS/route cleanup checked. External VPN interoperability and concurrent KVM load remain separate checks. |

## Known issues and remaining qualification

- **QHD + H.265 + WebRTC is unstable and can freeze or restart the device within seconds.** Use H.265 Direct for QHD. This specific combination is marked in the interface; the label does not apply to all QHD or all WebRTC modes.
- Earlier sustained H.265/CryptoDMA runs caused whole-device hangs. Their cause is unresolved. Watchdog recovery is not guaranteed for a bus/SoC lockup; physical power cycling may be required.
- Network responsiveness can degrade under load. A reported ping sample included 2–337 ms replies and repeated timeouts, without synchronized load/driver counters. It does not prove Wi-Fi disassociation. Paired idle/load/recovery browser, IRQ, queue and network measurements are still needed.
- Forced termination can leave native media buffers unusable. Application rollback is not a hardware reset.
- Fresh-card first boot, hardware Boot flashing of this exact image, power-loss recovery, Cube-specific operation, Realtek association and broad multi-browser endurance are not yet qualified.
- Larger PMTU budgets reduce packet count on confirmed paths, but IPv6/Tailscale/TURN browser behavior and route changes need broader qualification. Interface MTU alone is not proof of path MTU.

Report board/receiver revision, browser and platform, codec and transport, actual HDMI and stream dimensions, requested FPS, bitrate, duration and recovery behavior. Remove account data, tokens, private addresses and screen contents before sharing logs.
