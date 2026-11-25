/* memcpy, memmove, memset and memchr of the server, for the SG2002's C906 (XTheadVector, VLEN 128, misaligned
 * scalar access in hardware). Defined in the executable and not exported, they serve the server's own code (and the
 * libraries linked into it, libdatachannel among them); shared libraries such as the vendor media libraries keep
 * the C library's. Measured on the C906 against musl 1.2.6 (ns per call, 1200 bytes):
 * memcpy 873 -> 325 co-aligned and 1846 -> 340 misaligned, memset 300 -> 176, memchr 1880 -> 424.
 *
 * Below 32 bytes (copies) or 16 bytes (memset, memchr) scalar code without a loop: overlapping loads and stores
 * from both ends. From there XTheadVector loops of e8 elements (any address), LMUL 4 (64 bytes) for copies and
 * memchr, LMUL 8 for memset; one vector op moves what the single-issue core needs eight load/store pairs for.
 *
 * Contract: the memory these functions touch is cacheable. On the C906 vector loads and stores of uncached
 * (strongly ordered) memory fault, and misaligned scalar ones trap. The media buffers the server copies are
 * mapped cached (/dev/mem without O_SYNC; cvi-base and cvi-vi map kernel RAM): checked with every copy and memchr
 * of the process counted per mapping while it streamed WebRTC, MJPEG and the picture-channel preview. Not
 * exporting these keeps the vendor libraries, which map memory themselves, on the C library's aligned copies.
 *
 * Vector registers, vl and vtype are not preserved across calls (psABI); these functions call nothing. */
	.option arch, +xtheadbb, +xtheadvector
	.text

/* void *memcpy(void *d, const void *s, size_t n) */
	.globl	memcpy
	.type	memcpy, @function
	.p2align 6
memcpy:
	li	t6, 32
	bltu	a2, t6, .Lsmall_copy
	mv	a3, a0
1:	th.vsetvli t0, a2, e8, m4, d1
	th.vle.v v0, (a1)
	th.vse.v v0, (a3)
	add	a1, a1, t0
	add	a3, a3, t0
	sub	a2, a2, t0
	bnez	a2, 1b
	ret

/* 0-31 bytes: every load before any store, so memmove's overlapping copies use it too. */
.Lsmall_copy:
	add	a4, a1, a2
	add	a5, a0, a2
	li	t6, 16
	bltu	a2, t6, 2f
	ld	t0, 0(a1)
	ld	t1, 8(a1)
	ld	t2, -16(a4)
	ld	t3, -8(a4)
	sd	t0, 0(a0)
	sd	t1, 8(a0)
	sd	t2, -16(a5)
	sd	t3, -8(a5)
	ret
2:	li	t6, 8
	bltu	a2, t6, 3f
	ld	t0, 0(a1)
	ld	t1, -8(a4)
	sd	t0, 0(a0)
	sd	t1, -8(a5)
	ret
3:	li	t6, 4
	bltu	a2, t6, 4f
	lw	t0, 0(a1)
	lw	t1, -4(a4)
	sw	t0, 0(a0)
	sw	t1, -4(a5)
	ret
4:	beqz	a2, 6f
	li	t6, 2
	bltu	a2, t6, 5f
	lh	t0, 0(a1)
	lh	t1, -2(a4)
	sh	t0, 0(a0)
	sh	t1, -2(a5)
	ret
5:	lbu	t0, 0(a1)
	sb	t0, 0(a0)
6:	ret
	.size	memcpy, .-memcpy

/* void *memmove(void *d, const void *s, size_t n) */
	.globl	memmove
	.type	memmove, @function
	.p2align 6
memmove:
	sub	t0, a0, a1
	bltu	t0, a2, 1f		/* d inside [s, s+n): copy from the end */
	sub	t1, a1, a0
	bgeu	t1, a2, memcpy		/* no overlap */
	beqz	t1, 3f			/* d == s */
	li	t6, 32
	bltu	a2, t6, .Lsmall_copy
	/* d < s: forward; a chunk is loaded completely before it is stored, and later chunks lie above */
	mv	a3, a0
0:	th.vsetvli t0, a2, e8, m4, d1
	th.vle.v v0, (a1)
	th.vse.v v0, (a3)
	add	a1, a1, t0
	add	a3, a3, t0
	sub	a2, a2, t0
	bnez	a2, 0b
	ret
1:	li	t6, 32
	bltu	a2, t6, .Lsmall_copy
	/* d > s: backward from the end; the chunks still to load lie below each store */
	add	a4, a1, a2
	add	a5, a0, a2
2:	th.vsetvli t0, a2, e8, m4, d1
	sub	a4, a4, t0
	sub	a5, a5, t0
	th.vle.v v0, (a4)
	th.vse.v v0, (a5)
	sub	a2, a2, t0
	bnez	a2, 2b
3:	ret
	.size	memmove, .-memmove

/* void *memset(void *d, int c, size_t n) */
	.globl	memset
	.type	memset, @function
	.p2align 6
memset:
	li	t6, 16
	bltu	a2, t6, 2f
	mv	a3, a0
	th.vsetvli t0, a2, e8, m8, d1
	th.vmv.v.x v0, a1
1:	th.vsetvli t0, a2, e8, m8, d1
	th.vse.v v0, (a3)
	add	a3, a3, t0
	sub	a2, a2, t0
	bnez	a2, 1b
	ret
2:	andi	a1, a1, 0xff
	li	t0, 0x0101010101010101
	mul	a1, a1, t0
	add	a5, a0, a2
	li	t6, 8
	bltu	a2, t6, 3f
	sd	a1, 0(a0)
	sd	a1, -8(a5)
	ret
3:	li	t6, 4
	bltu	a2, t6, 4f
	sw	a1, 0(a0)
	sw	a1, -4(a5)
	ret
4:	beqz	a2, 5f
	sb	a1, 0(a0)
	sb	a1, -1(a5)
	li	t6, 3
	bltu	a2, t6, 5f
	sb	a1, 1(a0)
5:	ret
	.size	memset, .-memset

/* void *memchr(const void *s, int c, size_t n) */
	.globl	memchr
	.type	memchr, @function
	.p2align 6
memchr:
	andi	a1, a1, 0xff
	li	t6, 16
	bltu	a2, t6, 3f
1:	th.vsetvli t0, a2, e8, m4, d1
	th.vle.v v0, (a0)
	th.vmseq.vx v4, v0, a1
	th.vmfirst.m t1, v4
	bgez	t1, 2f
	add	a0, a0, t0
	sub	a2, a2, t0
	bnez	a2, 1b
	li	a0, 0
	ret
2:	add	a0, a0, t1
	ret
	/* 0-15 bytes: a word from each end (th.tstnbz marks the bytes equal to c), bytes below 8 */
3:	li	t6, 8
	bltu	a2, t6, 6f
	li	t0, 0x0101010101010101
	mul	t2, a1, t0
	ld	t1, 0(a0)
	xor	t1, t1, t2
	th.tstnbz t1, t1
	bnez	t1, 5f
	add	a0, a0, a2
	addi	a0, a0, -8		/* the last 8 bytes; those before them held no c */
	ld	t1, 0(a0)
	xor	t1, t1, t2
	th.tstnbz t1, t1
	bnez	t1, 5f
4:	li	a0, 0
	ret
5:	th.rev	t1, t1			/* lowest address first */
	th.ff1	t1, t1			/* leading zero bits */
	srli	t1, t1, 3
	add	a0, a0, t1
	ret
6:	beqz	a2, 4b
	lbu	t1, 0(a0)
	beq	t1, a1, 7f
	addi	a0, a0, 1
	addi	a2, a2, -1
	j	6b
7:	ret
	.size	memchr, .-memchr
