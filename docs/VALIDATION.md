# Beta-1 validation and known issues

This summary covers checks on the SG2002 NanoKVM PCIe with an LT6911UXC HDMI receiver. Cube and other board revisions are intended targets, not independently qualified hardware. Short checks, synthetic receivers and browser playback measure different things.

## What has been checked

| Area | Evidence and practical limit |
|---|---|
| Release assembly | FAT/ext4 structure, kernel/module correspondence, ZIP CRC, unpacked SHA-256, all 46 staged server/native/web files and four EDID profiles checked. The complete image has not been booted from a fresh card. |
| Application update | Signed sequence-3 application installed on the device; browser access and unique HTTPS certificate creation/reuse/0600 permissions checked. This does not test full-image flashing or power-loss recovery. |
| Video | H.264/H.265 Direct and WebRTC exercised in Chrome. FHD comparisons request **60 FPS**; QHD is capped at **30 FPS**. Encoder output and network frames are not browser presentation FPS or end-to-end latency. |
| HDMI recovery | Three capture-OFF/restart/enable cycles and one active-video restart passed with H.264 QHD in Chrome after the receiver-reset startup fix. This does not establish recovery from a whole-SoC hang. |
| CryptoDMA | All 14 standard `/dev/crypto` correctness cases matched software, with hardware completion counters proving DMA work. The extended diagnostic API passed 24 short algorithm/mode checks. Neither is an endurance qualification. |
| Kernel comparison | Matched 1024-operation, 16 KiB AES-192-CTR batches with video services stopped measured 48.11 / 48.13 / 48.85 MiB/s for 7.2.4 / 7.2.3 / 7.2.4. The earlier short-batch slowdown did not reproduce; universal speedup is not claimed. |
| T-Head instructions | Shipped ELF disassembly and isolated execution probes verified supported instructions. Flags alone are not proof of emitted instructions. Legacy XTheadVector is distinct from RVV 1.0; closed ISP objects were relinked, not rebuilt. |
| WebRTC PMTU | Chrome IPv4 LAN probing, silent-loss fallback and different budgets for two viewers checked. IPv6, TURN first-leg handling and ICE migration also have isolated transport tests; equivalent browser/tunnel/endurance coverage is incomplete. See [design and limits](webrtc-pmtu-design.md). |
| Wi-Fi | Exact SDIO alias selection and AIC8801 module reload/reconnect with Chrome recovery checked. RTL8733BS builds and is included, but association/reconnect on physical Realtek hardware remains untested. |
| OpenVPN DCO | Isolated on-device tunnel, traffic and restart checked. External VPN interoperability and concurrent KVM load remain separate checks. |

## Known issues and remaining qualification

- **QHD + H.265 + WebRTC is unstable and can freeze or restart the device within seconds.** Use H.265 Direct for QHD. This specific combination is marked in the interface; the label does not apply to all QHD or all WebRTC modes.
- Earlier sustained H.265/CryptoDMA runs caused whole-device hangs. Their cause is unresolved. Watchdog recovery is not guaranteed for a bus/SoC lockup; physical power cycling may be required.
- Network responsiveness can degrade under load. A reported ping sample included 2–337 ms replies and repeated timeouts, without synchronized load/driver counters. It does not prove Wi-Fi disassociation. Paired idle/load/recovery browser, IRQ, queue and network measurements are still needed.
- Forced termination can leave native media buffers unusable. Application rollback is not a hardware reset.
- Fresh-card first boot, hardware Boot flashing of this exact image, power-loss recovery, Cube-specific operation, Realtek association and broad multi-browser endurance are not yet qualified.
- Larger PMTU budgets reduce packet count on confirmed paths, but IPv6/Tailscale/TURN browser behavior and route changes need broader qualification. Interface MTU alone is not proof of path MTU.

Report board/receiver revision, browser and platform, codec and transport, actual HDMI and stream dimensions, requested FPS, bitrate, duration and recovery behavior. Remove account data, tokens, private addresses and screen contents before sharing logs.
