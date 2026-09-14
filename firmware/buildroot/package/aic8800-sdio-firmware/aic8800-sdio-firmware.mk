################################################################################
#
# aic8800-sdio-firmware
#
################################################################################

AIC8800_SDIO_FIRMWARE_VERSION = c56f910044cc854d6c553bcb9a644f3bca5a4c38
AIC8800_SDIO_FIRMWARE_SITE = $(call github,lxowalle,aic8800-sdio-firmware,$(AIC8800_SDIO_FIRMWARE_VERSION))

define AIC8800_SDIO_FIRMWARE_INSTALL_TARGET_CMDS
	mkdir -pv $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/
	rsync -r --verbose --copy-dirlinks --copy-links --hard-links ${@D}/* $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/
	# Use the qualified D80 H SDIO+BT image, preserving the chip-scoped layout.
	echo "f7fa0a1a296589568fbea1de89ab8feeb13ef2e6db6c7fafa682a34fb583a1bd  $(AIC8800_SDIO_FIRMWARE_PKGDIR)/files/fmacfwbt_8800d80_h_u02.bin" | sha256sum -c -
	$(INSTALL) -m 0644 $(AIC8800_SDIO_FIRMWARE_PKGDIR)/files/fmacfwbt_8800d80_h_u02.bin $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fmacfwbt_8800d80_h_u02.bin
	test -f $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fw_patch_table.bin
	test -f $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fw_patch_table_8800d80_u02.bin
	test -f $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fw_adid_8800d80_u02.bin
	test -f $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fw_patch_8800d80_u02.bin
	test -f $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fmacfwbt_8800d80_h_u02.bin
endef

$(eval $(generic-package))
