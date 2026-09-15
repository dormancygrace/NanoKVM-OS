# Beta-11 acceptance boundaries

The released full image is sequence 27. Its hashes and source input identity
are recorded in `firmware/release/beta11/build-manifest.json`.

Host image checks passed for the partition layout, FIT/kernel identity,
92 matched modules and rootfs content. The combined web build passed
TypeScript, production Vite compilation and five targeted OLED/terminal tests.

The preceding sequence-26 full-system package was installed from beta-10 on
the PCIe test device: settings and Python/hello addons were restored, normal
reboot completed, version/kernel/sequence were confirmed and server health
returned HTTP 200. OLED, DNS and USB Serial web fixes were subsequently
checked separately; the user confirmed their operation on their target devices.

The final combined sequence-27 image has not been flashed onto a fresh card.
This is not a fresh-card, direct beta-9 update, Cube OLED, physical HDD input,
power-loss recovery or broad hardware qualification claim.
Sequences 24 and 25 were withdrawn during WoL gadget filtering development.
Only the sequence-27 full image is distributed by the current release.
