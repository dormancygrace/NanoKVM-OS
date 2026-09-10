# AIC BSP ownership policy

The vendor BSP used a wildcard SDIO WLAN-class alias and a temporary probe could claim an unrelated primary function before checking IDs. sdio-ownership.patch and aicbsp_sdio_ids.h restrict both registration and probing to exact known pairs. The policy source hashes are in sdio-source.json.

Apply to the matching AIC8800 driver root:

```sh
python3 scripts/apply-aic-sdio-ownership.py --source /path/to/aic8800
python3 scripts/test-aic-sdio-ownership.py --source /path/to/aic8800
```

The full module builder applies this policy automatically. Inputs from a different BSP baseline fail explicitly and need review. The thirteen entries come from the existing chipmatch/secondary constants plus the actual NanoKVM AIC8801 secondary vendor/device544a:0146. Unknown vendor/device combinations are rejected. The policy does not change firmware loading or data-plane code for matched devices.

[Build, alias and actual AIC8801 reconnect evidence](../../../docs/VALIDATION.md). Coldboot, other chip variants, physical Realtek and Bluetooth behavior remain separately qualified requirements.
