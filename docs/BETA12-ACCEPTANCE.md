# Beta-12 acceptance boundaries

The full image is sequence 28. Host checks passed for FAT/ext4 layout,
embedded version, FIT/kernel identity and all 92 matching modules.
Targeted fixtures cover alpha/Cube/PCIe profile selection, PCIe fallback
when OLED is unavailable, optional I2C1 failure, HDMI address fallback and
fatal HDMI command failure without publishing new state.

The complete beta-12 image has not been device-flashed. The boot assets
remain Enhanced PCIe/UXC-oriented. A runtime Cube OLED-profile correction
does not establish full Cube boot/HDMI/ATX compatibility. Full Cube image
acceptance remains pending. Earlier accepted inputs are recorded in
[beta-11 acceptance](BETA11-ACCEPTANCE.md).
