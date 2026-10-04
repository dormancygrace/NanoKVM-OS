# SPDX-License-Identifier: GPL-2.0-only
obj-m += c906_vector_probe.o
c906_vector_probe-y := probe.o usercopy_xtheadvector.o memset_xtheadvector.o
ccflags-y += -O3 -fno-tree-vectorize -fno-tree-slp-vectorize
AFLAGS_usercopy_xtheadvector.o += -D__asm_xtheadvector_usercopy=probe_vector_usercopy
AFLAGS_usercopy_xtheadvector.o += -D__asm_xtheadvector_usercopy_sum_enabled=probe_vector_copy
