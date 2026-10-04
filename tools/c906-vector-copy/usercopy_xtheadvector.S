/* SPDX-License-Identifier: GPL-2.0-only */
/* Legacy RVV 0.7.1 byte copies. Call only while holding kernel vector state. */
#include <linux/linkage.h>
#include <asm/asm.h>
#include <asm/asm-extable.h>
#include <asm/csr.h>

	.option push
	.option arch, rv64imac_zicsr_zifencei_xtheadvector

	.macro thead_fixup op, reg, addr, target
100:
	\op \reg, \addr
	_asm_extable 100b, \target
	.endm

SYM_FUNC_START(__asm_xtheadvector_usercopy)
	li	t6, SR_SUM
	csrs	CSR_STATUS, t6
	mv	t6, ra
	call	__asm_xtheadvector_usercopy_sum_enabled
	mv	ra, t6
	li	t6, SR_SUM
	csrc	CSR_STATUS, t6
	ret
SYM_FUNC_END(__asm_xtheadvector_usercopy)

SYM_FUNC_START(__asm_xtheadvector_usercopy_sum_enabled)
	beqz	a2, .Lthead_done
.Lthead_copy:
	th.vsetvli a3, a2, e8, m8
	thead_fixup th.vlb.v, v0, (a1), .Lthead_done
	sub	a2, a2, a3
	add	a1, a1, a3
	thead_fixup th.vsb.v, v0, (a0), .Lthead_store_fault
	add	a0, a0, a3
	bnez	a2, .Lthead_copy
.Lthead_done:
	mv	a0, a2
	csrw	CSR_VSTART, zero
	ret
.Lthead_store_fault:
	/* e8 means vstart is exactly the count of stores completed. */
	add	a2, a2, a3
	csrr	t2, CSR_VSTART
	sub	a2, a2, t2
	j	.Lthead_done
SYM_FUNC_END(__asm_xtheadvector_usercopy_sum_enabled)
	.option pop
