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
	$(HOST_DIR)/bin/python3 \
		$(BR2_EXTERNAL_NANOKVM_PATH)/../../firmware/probes/edid120/build_experimental_edid.py \
		--input $(@D)/NanoKVM-QHD30.bin --output $(@D)/NanoKVM-720p120.bin --force
	$(HOST_DIR)/bin/python3 \
		$(BR2_EXTERNAL_NANOKVM_PATH)/../../firmware/probes/edid120/build_experimental_qhd40.py \
		--input $(@D)/NanoKVM-720p120.bin --output $(@D)/NanoKVM-QHD40.bin --force
	$(HOST_DIR)/bin/python3 \
		$(BR2_EXTERNAL_NANOKVM_PATH)/../../firmware/probes/edid120/build_experimental_fhd_high.py \
		--rate 75 --input $(@D)/NanoKVM-QHD40.bin \
		--output $(@D)/NanoKVM-final-video-profiles.bin --force
	$(HOST_DIR)/bin/python3 \
		$(BR2_EXTERNAL_NANOKVM_PATH)/../../firmware/probes/edid120/build_final_monitor_profiles.py \
		--input $(@D)/NanoKVM-final-video-profiles.bin \
		--output $(@D)/monitor-profiles
	$(HOST_DIR)/bin/python3 $(BR2_EXTERNAL_NANOKVM_PATH)/../../scripts/build-portrait-edid.py \
		--input $(@D)/E21_NanoKVM.bin --output $(@D)/NanoKVM-portrait-1080x1920.bin
	$(HOST_DIR)/bin/python3 $(BR2_EXTERNAL_NANOKVM_PATH)/../../scripts/build-portrait-edid.py \
		--profile hd --input $(@D)/E21_NanoKVM.bin --output $(@D)/NanoKVM-portrait-720x1280.bin
	$(HOST_DIR)/bin/python3 $(BR2_EXTERNAL_NANOKVM_PATH)/../../scripts/build-portrait-edid.py \
		--profile h264 --input $(@D)/E21_NanoKVM.bin --output $(@D)/NanoKVM-portrait-1296x2304.bin
	$(HOST_DIR)/bin/python3 $(BR2_EXTERNAL_NANOKVM_PATH)/../../scripts/build-portrait-edid.py \
		--profile max --input $(@D)/E21_NanoKVM.bin --output $(@D)/NanoKVM-portrait-1440x2560.bin
	# Never reuse the historical checked-in executable: its malformed ELF
	# program headers cannot be repaired by target stripping.
	rm -f $(@D)/nanokvm_update_edid
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
	$(INSTALL) -D -m 0644 \
		$(@D)/monitor-profiles/NanoKVM-monitor-auto.bin \
		$(TARGET_DIR)/usr/share/nanokvm/edid/NanoKVM-final-video-profiles.bin
	$(INSTALL) -m 0644 $(@D)/monitor-profiles/NanoKVM-monitor-*.bin $(TARGET_DIR)/usr/share/nanokvm/edid/
	$(INSTALL) -m 0644 $(@D)/monitor-profiles/NanoKVM-cube-monitor-*.bin $(TARGET_DIR)/usr/share/nanokvm/edid/
	$(INSTALL) -D -m 0644 $(@D)/NanoKVM-portrait-1080x1920.bin \
		$(TARGET_DIR)/usr/share/nanokvm/edid/NanoKVM-portrait-1080x1920.bin
	$(INSTALL) -D -m 0644 $(@D)/NanoKVM-portrait-720x1280.bin \
		$(TARGET_DIR)/usr/share/nanokvm/edid/NanoKVM-portrait-720x1280.bin
	$(INSTALL) -D -m 0644 $(@D)/NanoKVM-portrait-1296x2304.bin \
		$(TARGET_DIR)/usr/share/nanokvm/edid/NanoKVM-portrait-1296x2304.bin
	$(INSTALL) -D -m 0644 $(@D)/NanoKVM-portrait-1440x2560.bin \
		$(TARGET_DIR)/usr/share/nanokvm/edid/NanoKVM-portrait-1440x2560.bin
	$(INSTALL) -D -m 0644 $(@D)/E21_NanoKVM.bin \
		$(TARGET_DIR)/usr/share/nanokvm/edid/NanoKVM-stock.bin
endef

$(eval $(generic-package))
