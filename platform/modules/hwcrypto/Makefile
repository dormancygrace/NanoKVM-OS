obj-m += nanokvm_hwcrypto.o
# nanokvm_c906l.h: the small core's GHASH for gcm(aes) (platform/build.sh gives C906L_DIR).
$(if $(C906L_DIR),,$(error C906L_DIR not set))
ccflags-y += -I$(C906L_DIR)
