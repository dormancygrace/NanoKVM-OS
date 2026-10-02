# NanoKVM U-Boot 2026.07

The second-stage loader in `fip.bin` of every NanoKVM OS v2 image is upstream U-Boot v2026.07 with three board patches. It reports itself as `U-Boot 2026.07-00003-ga5149dd2536c ... NanoKVM Enhanced`. The rest of the FIP (FSBL/BL2, DDR parameters and OpenSBI 0.9) is carried over unchanged from the stock Sipeed NanoKVM FIP; see [SOURCE.md](../../docs/SOURCE.md).

Changes relative to upstream v2026.07:

1. `0001`: configure the low-speed SD PHY TX/RX clocks and add the CV1800B sysreset node.
2. `0002`: apply the board MMC limits after the generic SDHCI capabilities, so `no-1-8-v` and the 25 MHz limit are respected and no CMD11 voltage switch is attempted.
3. `0003`: power up and identify the SG2002 internal Ethernet PHY before handing over to Linux, as the vendor U-Boot did (`cv181x_ephy_id_init`).
4. `nanokvm_enhanced_defconfig`: board init, zstd, a three-second interruptible autoboot, and a boot command that loads `/boot/boot.sd` from the FAT partition to `0x85000000` and boots its SG2002 configuration with the normal root arguments.

Build `u-boot.bin` with `scripts/build-enhanced-uboot.py` or the release recipe in [source-components/uboot](../release/source-components/uboot/README.md). Package it with `scripts/build-enhanced-ram-fip.py --base-fip <stock fip.bin> --base-sha256 4a40ec182cad2606e8d41acae6da0953a89dd47e4e96e5ea95e803f09fd9f4db --uboot u-boot.bin ...`. Without `--opensbi` the builder keeps BL2, DDR parameters, BLCP and OpenSBI byte for byte and replaces only LOADER_2ND. The release FIP has SHA-256 `8254d38124e66877ab6c4a894f656d8ee6dc299d813dd06f31037ddaa2e51b9d`.

U-Boot's own Ethernet/TFTP is not used ("No ethernet found" at boot refers to U-Boot's minimal network setup); Linux drives the PHY.
