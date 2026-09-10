NANOKVM_VENDOR_LIBC_VERSION = fb0a6ac7409fb477c5f46ced75bf05527def7cb9
NANOKVM_VENDOR_LIBC_SITE = $(call github,0x754C,cvitek-riscv64-musl-sysroot,$(NANOKVM_VENDOR_LIBC_VERSION))
NANOKVM_VENDOR_LIBC_LICENSE = MIT

define NANOKVM_VENDOR_LIBC_INSTALL_TARGET_CMDS
	$(INSTALL) -d $(TARGET_DIR)/usr/lib
	for loader in $(@D)/usr/lib/ld-musl-*xthead*.so.1; do \
		$(INSTALL) -m755 $$loader $(TARGET_DIR)/usr/lib/; \
	done
endef
$(eval $(generic-package))
