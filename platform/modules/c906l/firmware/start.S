/* SPDX-License-Identifier: GPL-2.0-only */
/*
 * Entry of the small core's program, at the start of its memory. The program
 * is a static PIE linked at 0 (fw.ld); the driver loads it anywhere, so the
 * R_RISCV_RELATIVE relocations are applied here before anything uses a
 * pointer from data. Caches, branch prediction and data prefetch on as the
 * vendor's FreeRTOS start (mcor, mhcr, mhint are T-Head CSRs), stack, zeroed
 * bss, then c906l_main(memory).
 */
	.section .text.start, "ax"
	.globl _start
_start:
	li	t0, 0x30013		/* mcor: invalidate I/D caches, BHT, BTB */
	csrs	0x7c2, t0
	li	t0, 0x7f		/* mhcr: I/D caches, write-allocate, write-back, RAS, BPE, BTB */
	csrs	0x7c1, t0
	li	t0, 0x610c		/* mhint: D-cache prefetch, AMR */
	csrs	0x7c5, t0
	li	t0, 1 << 13		/* mstatus.FS: the FPU for Opus */
	csrs	mstatus, t0
	fscsr	zero
	lla	a0, _start
	lla	t0, __rela_start
	lla	t1, __rela_end
1:	bgeu	t0, t1, 2f
	ld	t2, 0(t0)		/* r_offset */
	ld	t3, 8(t0)		/* r_info */
	ld	t4, 16(t0)		/* r_addend */
	li	t5, 3			/* R_RISCV_RELATIVE, the only kind the build allows */
	bne	t3, t5, 9f
	add	t2, t2, a0
	add	t4, t4, a0
	sd	t4, 0(t2)
	addi	t0, t0, 24
	j	1b
2:	lla	t0, __bss_start
	lla	t1, __bss_end
3:	bgeu	t0, t1, 4f
	sd	zero, 0(t0)
	addi	t0, t0, 8
	j	3b
4:	lla	sp, __stack_top
	call	c906l_main
9:	wfi
	j	9b
