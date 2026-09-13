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
	test -f $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fw_patch_table.bin
	test -f $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fw_patch_table_8800d80_u02.bin
	test -f $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fw_adid_8800d80_u02.bin
	test -f $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fw_patch_8800d80_u02.bin
	test -f $(TARGET_DIR)/usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fmacfwbt_8800d80_h_u02.bin
endef

$(eval $(generic-package))
