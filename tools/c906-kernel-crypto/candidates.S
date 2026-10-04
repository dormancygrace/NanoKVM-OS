/* SPDX-License-Identifier: GPL-2.0-only */
#include <linux/linkage.h>
	.option push
	.option arch, rv64imac_zicsr_zifencei_xtheadvector
	.text
/* VLEN128/ELEN32: four independent blocks; only aligned scratch stores.
 * All rotations use legacy shifts/OR, without modern vector crypto. */
	.macro QR a,b,c,d
	th.vadd.vv \a, \a, \b
	th.vxor.vv \d, \d, \a
	th.vsll.vi v16, \d, 16
	th.vsrl.vi \d, \d, 16
	th.vor.vv \d, \d, v16
	th.vadd.vv \c, \c, \d
	th.vxor.vv \b, \b, \c
	th.vsll.vi v16, \b, 12
	th.vsrl.vi \b, \b, 20
	th.vor.vv \b, \b, v16
	th.vadd.vv \a, \a, \b
	th.vxor.vv \d, \d, \a
	th.vsll.vi v16, \d, 8
	th.vsrl.vi \d, \d, 24
	th.vor.vv \d, \d, v16
	th.vadd.vv \c, \c, \d
	th.vxor.vv \b, \b, \c
	th.vsll.vi v16, \b, 7
	th.vsrl.vi \b, \b, 25
	th.vor.vv \b, \b, v16
	.endm
	.macro LOAD word,reg
	lwu t0, (4*\word)(a0)
	th.vmv.v.x \reg,t0
	.endm
	.macro OUTPUT word,reg
	lwu t0,(4*\word)(a0)
	th.vadd.vx \reg,\reg,t0
	.if \word == 12
	th.vadd.vv \reg,\reg,v17
	.endif
	addi t1,a1,(4*\word)
	th.vssw.v \reg,(t1),t2
	.endm
SYM_FUNC_START(kc_chacha_blocks)
	th.vsetvli zero,a2,e32,m1
	LOAD 0,v0
	LOAD 1,v1
	LOAD 2,v2
	LOAD 3,v3
	LOAD 4,v4
	LOAD 5,v5
	LOAD 6,v6
	LOAD 7,v7
	LOAD 8,v8
	LOAD 9,v9
	LOAD 10,v10
	LOAD 11,v11
	LOAD 12,v12
	LOAD 13,v13
	LOAD 14,v14
	LOAD 15,v15
	th.vid.v v17
	th.vadd.vv v12,v12,v17
	mv t3,a3
.Lrounds:
	QR v0,v4,v8,v12
	QR v1,v5,v9,v13
	QR v2,v6,v10,v14
	QR v3,v7,v11,v15
	QR v0,v5,v10,v15
	QR v1,v6,v11,v12
	QR v2,v7,v8,v13
	QR v3,v4,v9,v14
	addi t3,t3,-2
	bnez t3,.Lrounds
	li t2,64
	OUTPUT 0,v0
	OUTPUT 1,v1
	OUTPUT 2,v2
	OUTPUT 3,v3
	OUTPUT 4,v4
	OUTPUT 5,v5
	OUTPUT 6,v6
	OUTPUT 7,v7
	OUTPUT 8,v8
	OUTPUT 9,v9
	OUTPUT 10,v10
	OUTPUT 11,v11
	OUTPUT 12,v12
	OUTPUT 13,v13
	OUTPUT 14,v14
	OUTPUT 15,v15
	lwu t0,48(a0)
	addw t0,t0,a2
	sw t0,48(a0)
	ret
SYM_FUNC_END(kc_chacha_blocks)
SYM_FUNC_START(kc_xor_bytes)
	beqz a3,.Lxor_done
.Lxor_loop:
	th.vsetvli t0,a3,e8,m8
	th.vlb.v v0,(a1)
	th.vlb.v v8,(a2)
	th.vxor.vv v16,v0,v8
	th.vsb.v v16,(a0)
	add a0,a0,t0
	add a1,a1,t0
	add a2,a2,t0
	sub a3,a3,t0
	bnez a3,.Lxor_loop
.Lxor_done:
	ret
SYM_FUNC_END(kc_xor_bytes)
/* Parallel reflected CRC streams, each exactly 256 bytes, 2..16 lanes.
 * a0=input a1=table0 a2=output u32[] a3=lanes. Indexed byte loads
 * are zero-extended to SEW32. Each lookup index is a byte offset. */
SYM_FUNC_START(kc_crc_lanes)
	th.vsetvli zero,a3,e32,m4
	th.vmv.v.i v0,0
	th.vid.v v4
	th.vsll.vi v4,v4,8
	li t0,256
.Lcrc_loop:
	th.vlxbu.v v8,(a0),v4
	th.vxor.vv v8,v0,v8
	li t1,255
	th.vand.vx v8,v8,t1
	th.vsll.vi v8,v8,2
	th.vlxwu.v v12,(a1),v8
	th.vsrl.vi v0,v0,8
	th.vxor.vv v0,v0,v12
	th.vadd.vi v4,v4,1
	addi t0,t0,-1
	bnez t0,.Lcrc_loop
	th.vsw.v v0,(a2)
	ret
SYM_FUNC_END(kc_crc_lanes)
/* Copy byte-packed vectors and reduce them as little-endian halfwords.
 * Neither source nor destination needs word alignment. */
SYM_FUNC_START(kc_copy_csum)
	li t0,0
	li t1,1
	th.vsetvli zero,t1,e32,m1
	th.vmv.s.x v8,zero
.Lcopy_loop:
	li t1,2
	bltu a2,t1,.Lcopy_tail
	andi t1,a2,-2
	th.vsetvli t2,t1,e8,m8
	th.vlb.v v0,(a1)
	th.vsb.v v0,(a0)
	srli t1,t2,1
	th.vsetvli zero,t1,e16,m8
	th.vwredsumu.vs v8,v0,v8
	add a0,a0,t2
	add a1,a1,t2
	sub a2,a2,t2
	j .Lcopy_loop
.Lcopy_tail:
	beqz a2,.Lcopy_fold
	lbu t0,0(a1)
	sb t0,0(a0)
.Lcopy_fold:
	li t1,1
	th.vsetvli zero,t1,e32,m1
	th.vmv.x.s t1,v8
	add t0,t0,t1
	li t1,65535
	and t2,t0,t1
	srli t0,t0,16
	add t0,t0,t2
	and t2,t0,t1
	srli t0,t0,16
	add a0,t0,t2
	ret
SYM_FUNC_END(kc_copy_csum)
	.option pop
