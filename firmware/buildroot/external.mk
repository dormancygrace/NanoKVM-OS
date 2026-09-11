include $(sort $(wildcard $(BR2_EXTERNAL_NANOKVM_PATH)/package/*/*.mk))

# Kernel hwprobe distinguishes XTheadVector from standard V. OpenSSL 3.6.4
# reports RV64GC on the tested C906: scalar assembly is enabled, RVV 1.0 is not.
LIBOPENSSL_TARGET_ARCH = linux64-riscv64 enable-devcryptoeng no-ktls
# The kernel is built separately. Install the pinned public cryptodev ABI header
# without enabling Buildroot's in-tree Linux-kernel package dependency.
define NANOKVM_INSTALL_CRYPTODEV_HEADER
	$(INSTALL) -D -m 0644 $(BR2_EXTERNAL_NANOKVM_PATH)/../crypto/cryptodev-linux/crypto/cryptodev.h $(STAGING_DIR)/usr/include/crypto/cryptodev.h
endef
LIBOPENSSL_PRE_CONFIGURE_HOOKS += NANOKVM_INSTALL_CRYPTODEV_HEADER
# Buildroot applies BR2_TARGET_OPTIMIZATION through its compiler wrapper, but
# GCC builds libstdc++/libgomp with xgcc directly. Propagate the same C906 flags
# there too; otherwise those runtime libraries still contain fence.tso.
GCC_COMMON_TARGET_CFLAGS += $(call qstrip,$(BR2_TARGET_OPTIMIZATION))
GCC_COMMON_TARGET_CXXFLAGS += $(call qstrip,$(BR2_TARGET_OPTIMIZATION))

# Do not expose host toolchain paths through mc --configure-options.
MC_CONF_OPTS += --disable-configure-args

# Configure runs on the host; helper shebangs must name the target interpreter.
MC_CONF_ENV += ac_cv_path_PYTHON=/usr/bin/python3
