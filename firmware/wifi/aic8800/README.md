# AIC BSP ownership policy

The vendor BSP used a wildcard SDIO WLAN-class alias and a temporary probe could claim an unrelated primary function before checking IDs. sdio-ownership.patch and aicbsp_sdio_ids.h restrict both registration and probing to exact known pairs. The policy source hashes are in sdio-source.json.

Apply to the matching AIC8800 driver root:

```sh
python3 scripts/apply-aic-sdio-ownership.py --source /path/to/aic8800
python3 scripts/test-aic-sdio-ownership.py --source /path/to/aic8800
```

The full module builder applies this policy automatically. Inputs from a different BSP baseline fail explicitly and need review. The thirteen entries come from the existing chipmatch/secondary constants plus the actual NanoKVM AIC8801 secondary vendor/device544a:0146. Unknown vendor/device combinations are rejected. The policy does not change firmware loading or data-plane code for matched devices.

## Survey-frequency guard (2026-09-16)

The shared SDIO message handler previously called `BUG_ON(1)` when firmware sent a survey frequency absent from the registered 2.4/5 GHz channel list. This is the SDIO counterpart of the path reported in [Radxa issue #92](https://github.com/radxa-pkg/aic8800/issues/92). That report concerns USB FullMAC; this SDIO-only change does not establish a fix for the USB driver. It also accepted an index equal to the `survey` array length. `survey-frequency-guard.patch` returns `-ENOENT` for an unknown frequency, logs it with rate limiting, and discards negative or out-of-range survey indices. The module builder applies the exact-hash-checked patch to both AIC8801 and AIC8800D80 builds.

`scripts/test-aic-survey-frequency-guard.py --source-file <pristine-rwnx_msg_rx.c>` compiles the actual patched lookup and survey handler under ASan/UBSan. It tests 2.4/5 GHz channels, an unknown frequency, the one-past-end index, repeat application and rejection of unrelated source.

The complete Wi-Fi/BT module built and modposted for `7.2.5-nanokvm-os-r3`. A one-shot RAM-only UART trial on the local AIC8801 (`5449:0145`) loaded the patched module, reconnected at 5180 MHz, returned survey data on both bands, and passed three gateway pings. The script then restored the original disk-backed module; its `srcversion` and file SHA-256 were verified afterwards, Wi-Fi was again connected at 5180 MHz, and boot ID was unchanged. No malformed firmware event was injected on hardware, and D80 has not been retested with this patch. The on-device persistent module and release image were not changed.

[Build, alias and actual AIC8801 reconnect evidence](../../release/evidence/2026-09-10-aic-sdio/README.md). Coldboot, other chip variants, physical Realtek and Bluetooth behavior remain separately qualified requirements.

## Linux 7.3 source compatibility (2026-10-01)

The standard preparation chain now also applies `linux-7.3-sdio.patch`, the
SDIO-only backport of [Radxa eae57ef2](https://github.com/radxa-pkg/aic8800/commit/eae57ef2297ce7b897549173ea787f01f35631df).
It adapts cfg80211 probe status and preserves cfg80211-assigned RoC, management
TX and probe cookies across asynchronous completion. Version guards retain the
older API paths. `linux-7.3-source.json` records all six inputs and outputs;
unknown or partially patched inputs are rejected before compatibility writes.
The production kernel remains `7.2.6-nanokvm-os-r1`.

The survey patch has corrected diff context and is now applied with `patch`
without fuzz, including beneath an ignored `work/` directory. Its resulting
source hash is unchanged. Reapplying the full chain accepts the combined survey
and Linux 7.3 source. Run `scripts/test-upstream-source-patches.py` with pristine
MaixCDK and SDIO inputs to verify application, repetition and input rejection.
See [integration and verification](../../UPSTREAM-FIXES-2026-10-01.md).
