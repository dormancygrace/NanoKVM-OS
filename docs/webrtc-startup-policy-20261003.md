# WebRTC startup policy after app-only updates

On 2026-10-03, a cold boot of the device with app 2.0_beta8-r17 and kernel
7.2.6 started NanoKVM-Server without the six app sender defaults. The process
environment contained the older native and sysmon settings, but no queue32,
batch1, flush1000us, GSO1, hardware AES selection or explicit GOMAXPROCS1.

OpenRC nanokvm-app invoked /usr/libexec/nanokvm/legacy/S95nanokvm, which belongs
to the base package. An app-only update replaces the app-owned
/kvmapp/system/init.d/S95nanokvm, while leaving that base copy unchanged.
The app-owned script already has the accepted sender defaults.

OpenRC now invokes the app-owned S95 for both start and stop. Startup policy
therefore follows the installed application version after an app-only update.
Hardware dependencies and stop/error behavior are unchanged. The legacy base S95 is now a symlink to the app-owned script, in both package
assembly paths. The compatibility entry points therefore follow app-only updates
as well, rather than retaining an independent copied startup policy.

Shell syntax, the existing OpenRC dependency/failure checks, service-stop
behavior tests and the packaging regression passed. The packaging fixture starts
with a stale base S95 and confirms that replacing only the app policy is visible
through the base compatibility symlink. This change is source-only for future v2.1-b1: the new
OpenRC wrapper has not been installed or qualified through a device cold boot.

The historical best r15 diagnostic build also used the prepared Go 1.27.1
runtime with the sysmon timer-slack hooks, whereas normal r17 used standard Go.
S95 exports NANOKVM_SYSMON_TIMER_SLACK_NS=250000. Standard Go cannot apply this
custom setting. Its contribution to the performance difference is unmeasured;
do not attribute the whole regression to it or promote another runtime without
a controlled comparison.

## Bounded live comparison on the unchanged installed server

Both runs used app r17 SHA256
71cd2696254bf1392ba69778c9d11d68d6a663d1da50f2516a44d4ee31b0533b,
the same boot e0cc3075-5027-41a4-b843-53b76e1414f2 and native bundle,
H.264 1920x1080 / 60 requested FPS / 10 Mbps, Wi-Fi power save on.
No Ethernet or WireGuard video traffic was measured, both selected browser
candidate pairs used the device LAN address, and both receivers reported zero RTP loss.
The CPU and browser observation windows differ; exact windows are in the raw
records and these values are not claimed as synchronized per-frame samples.

| Startup | Viewers | Decoded FPS | Process CPU | Total CPU | Encoded FPS |
| --- | --- | --- | --- | --- | --- |
| Existing cold-boot base S95 | 1 | 55.63 | 65.47% | 74.05% | 55.37 |
| Existing cold-boot base S95 | 2 | 40.79 / 40.80 | 60.97% | 69.69% | 41.19 |
| Existing app S95, accepted defaults | 1 | 58.87 | 51.58% | 57.18% | 58.42 |
| Existing app S95, accepted defaults | 2 | 50.69 / 50.76 | 69.38% | 78.79% | 49.89 |

A direct restart through the already-installed app S95 activated its existing
defaults; no package or new startup file was installed. The two-viewer target
is still not reached. The previous 55-56 FPS diagnostic result is not reproduced
by this normal standard-Go server. Hardware AES stability beyond these bounded
windows is not established.

Host CPU records: work/perf-webrtc-20261003/r17_cold_boot_{one,two}.json and
r17_app_owned_{one,two}.json. Browser records:
the r17 browser result files (kept outside the repository).
