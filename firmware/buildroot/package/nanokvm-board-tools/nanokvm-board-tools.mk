# Board maintenance tools from the same pinned NanoKVM Enhanced source tree.
NANOKVM_BOARD_TOOLS_VERSION = 1
NANOKVM_BOARD_TOOLS_SITE = $(BR2_EXTERNAL_NANOKVM_PATH)/../../tools/nanokvm_update_edid
NANOKVM_BOARD_TOOLS_SITE_METHOD = local
NANOKVM_BOARD_TOOLS_DEPENDENCIES = host-python3
NANOKVM_BOARD_TOOLS_LICENSE = GPL-3.0
NANOKVM_BOARD_TOOLS_LICENSE_FILES = LICENSE

define NANOKVM_BOARD_TOOLS_BUILD_CMDS
	# Generated assets live only in the package build directory.
	rm -f $(@D)/NanoKVM-QHD30.bin $(@D)/NanoKVM-QHD30.bin.json
	$(HOST_DIR)/bin/python3 $(BR2_EXTERNAL_NANOKVM_PATH)/../../scripts/build-qhd-edid.py \
		--input $(@D)/E21_NanoKVM.bin --output $(@D)/NanoKVM-QHD30.bin
	$(HOST_DIR)/bin/python3 $(BR2_EXTERNAL_NANOKVM_PATH)/../../scripts/build-monitor-edids.py \
		--input $(@D)/E21_NanoKVM.bin --output $(@D)/monitor-profiles
	$(TARGET_MAKE_ENV) $(MAKE) -C $(@D) CC="$(TARGET_CC)" \
		CFLAGS="$(TARGET_CFLAGS) -Wall -Wextra -Werror" RISCV_FLAGS= \
		LDFLAGS="$(TARGET_LDFLAGS)"
endef

define NANOKVM_BOARD_TOOLS_INSTALL_TARGET_CMDS
	rm -f $(TARGET_DIR)/usr/share/nanokvm/edid/NanoKVM-monitor-480.bin
	$(INSTALL) -D -m 0755 $(@D)/nanokvm_update_edid \
		$(TARGET_DIR)/usr/sbin/nanokvm_update_edid
	$(INSTALL) -D -m 0644 $(@D)/NanoKVM-QHD30.bin \
		$(TARGET_DIR)/usr/share/nanokvm/edid/NanoKVM-QHD30.bin
	$(INSTALL) -m 0644 $(@D)/monitor-profiles/NanoKVM-monitor-*.bin $(TARGET_DIR)/usr/share/nanokvm/edid/
	$(INSTALL) -D -m 0644 $(@D)/E21_NanoKVM.bin \
		$(TARGET_DIR)/usr/share/nanokvm/edid/NanoKVM-stock.bin
endef

$(eval $(generic-package))
