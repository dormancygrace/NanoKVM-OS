include $(sort $(wildcard $(BR2_EXTERNAL_NANOKVM_PATH)/package/*/*.mk))

# Buildroot applies BR2_TARGET_OPTIMIZATION through its compiler wrapper, but
# GCC builds libstdc++/libgomp with xgcc directly. Propagate the same C906 flags
# there too; otherwise those runtime libraries still contain fence.tso.
GCC_COMMON_TARGET_CFLAGS += $(call qstrip,$(BR2_TARGET_OPTIMIZATION))
GCC_COMMON_TARGET_CXXFLAGS += $(call qstrip,$(BR2_TARGET_OPTIMIZATION))

# The boot initramfs carries fsck.f2fs on its own. Link libf2fs statically so
# the program needs no extra library and no build-directory RPATH, which would
# make the initramfs depend on where it was built.
F2FS_TOOLS_CONF_OPTS += --disable-shared --enable-static
