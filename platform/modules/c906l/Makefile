obj-m += nanokvm_c906l.o

# C906L_FW: the small core's program, built by scripts/build-c906l-firmware.py
# (platform/build.sh gives the path).
$(if $(C906L_FW),,$(error C906L_FW not set))
$(obj)/nanokvm_c906l.o: $(C906L_FW)
CFLAGS_nanokvm_c906l.o += -DC906L_FW='"$(C906L_FW)"'
