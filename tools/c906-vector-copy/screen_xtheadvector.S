/* SPDX-License-Identifier: GPL-2.0-only */
#include <linux/linkage.h>
#include <asm/asm.h>
	.option push
	.option arch, rv64imac_zicsr_zifencei_xtheadvector
/* a0/a1=byte arrays, a2=length; bounded, exact first differing byte. */
SYM_FUNC_START(screen_vector_prefix)
	mv	t0, a2
	beqz	a2, .Lprefix_done
.Lprefix_loop:
	th.vsetvli a3, a2, e8, m8
	th.vlb.v v0, (a0)
	th.vlb.v v8, (a1)
	th.vmsne.vv v16, v0, v8
	th.vmfirst.m a4, v16
	bgez	a4, .Lprefix_found
	add	a0, a0, a3
	add	a1, a1, a3
	sub	a2, a2, a3
	bnez	a2, .Lprefix_loop
.Lprefix_done:
	mv	a0, t0
	ret
.Lprefix_found:
	sub	a0, t0, a2
	add	a0, a0, a4
	ret
SYM_FUNC_END(screen_vector_prefix)

/* Return do_csum's uncomplemented 16-bit little-endian sum.
 * Aligned halfword loads; odd first-byte alignment is handled explicitly.
 * Each widening reduction batch <=32768 halfwords, so its 32-bit seed
 * cannot overflow or set the sign bit even for all-0xffff input.
 */
SYM_FUNC_START(screen_vector_csum)
	li	t0, 0
	andi	t1, a0, 1
	beqz	a1, .Lcsum_zero
	beqz	t1, .Lcsum_blocks
	lbu	t0, 0(a0)
	slli	t0, t0, 8
	addi	a0, a0, 1
	addi	a1, a1, -1
.Lcsum_blocks:
	srli	t2, a1, 1
	beqz	t2, .Lcsum_tail
	li	t3, 32768
	bleu	t2, t3, .Lcsum_batch
	mv	t2, t3
.Lcsum_batch:
	li	t3, 1
	th.vsetvli zero, t3, e32, m1
	th.vmv.s.x v8, zero
	slli	t4, t2, 1
	sub	a1, a1, t4
.Lcsum_loop:
	th.vsetvli t3, t2, e16, m8
	th.vlh.v v0, (a0)
	th.vwredsumu.vs v8, v0, v8
	slli	t4, t3, 1
	add	a0, a0, t4
	sub	t2, t2, t3
	bnez	t2, .Lcsum_loop
	li	t3, 1
	th.vsetvli zero, t3, e32, m1
	th.vmv.x.s t3, v8
	add	t0, t0, t3
	li	t3, 2
	bgeu	a1, t3, .Lcsum_blocks
.Lcsum_tail:
	beqz	a1, .Lcsum_fold
	lbu	t3, 0(a0)
	add	t0, t0, t3
.Lcsum_fold:
	li	t3, 65535
	and	t4, t0, t3
	srli	t0, t0, 16
	add	t0, t0, t4
	and	t4, t0, t3
	srli	t0, t0, 16
	add	t0, t0, t4
	and	t4, t0, t3
	srli	t0, t0, 16
	add	t0, t0, t4
	beqz	t1, .Lcsum_zero
	srli	t4, t0, 8
	slli	t0, t0, 8
	or	t0, t0, t4
	and	t0, t0, t3
.Lcsum_zero:
	mv	a0, t0
	ret
SYM_FUNC_END(screen_vector_csum)
	.option pop
