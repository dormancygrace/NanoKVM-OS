CROSS_COMPILE ?= riscv64-buildroot-linux-musl-
CC = $(CROSS_COMPILE)gcc
CFLAGS ?= -O2 -Wall -Wextra
RISCV_FLAGS ?= -march=rv64gc -mabi=lp64d -mtune=thead-c906 -mno-fence-tso
TARGET = nanokvm_update_edid

all: $(TARGET)

$(TARGET): nanokvm_update_edid.c nanokvm_update_edid.h
	$(CC) $(CPPFLAGS) $(CFLAGS) $(RISCV_FLAGS) $(LDFLAGS) -o $@ nanokvm_update_edid.c $(LDLIBS)

clean:
	rm -f $(TARGET)

.PHONY: all clean
