/* SPDX-License-Identifier: GPL-2.0-only */
#include <linux/linkage.h>
#include <asm/asm.h>
	.option push
	.option arch, rv64imac_zicsr_zifencei_xtheadvector
SYM_FUNC_START(probe_vector_memset)
	beqz	a2, .Lfill_done
.Lfill_loop:
	th.vsetvli a3, a2, e8, m8
	th.vmv.v.x v0, a1
	th.vsb.v v0, (a0)
	sub	a2, a2, a3
	add	a0, a0, a3
	bnez	a2, .Lfill_loop
.Lfill_done:
	ret
SYM_FUNC_END(probe_vector_memset)
	.option pop
