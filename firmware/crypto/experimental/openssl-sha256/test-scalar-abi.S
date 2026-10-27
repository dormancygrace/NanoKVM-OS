/* SPDX-License-Identifier: Apache-2.0 */
.text
.globl _start
_start:
call main
li a7, 93
ecall
.globl checked_sha256
checked_sha256:
addi sp, sp, -112
sd ra, 96(sp)
sd s0, 0(sp)
sd s1, 8(sp)
sd s2, 16(sp)
sd s3, 24(sp)
sd s4, 32(sp)
sd s5, 40(sp)
sd s6, 48(sp)
sd s7, 56(sp)
sd s8, 64(sp)
sd s9, 72(sp)
sd s10, 80(sp)
sd s11, 88(sp)
sd a2, 104(sp)
li s0, 1296
li s1, 1297
li s2, 1298
li s3, 1299
li s4, 1300
li s5, 1301
li s6, 1302
li s7, 1303
li s8, 1304
li s9, 1305
li s10, 1306
li s11, 1307
call ossl_sha256_c906_blocks
li a0, 0
li t0, 1296
bne s0, t0, .Lbad
li t0, 1297
bne s1, t0, .Lbad
li t0, 1298
bne s2, t0, .Lbad
li t0, 1299
bne s3, t0, .Lbad
li t0, 1300
bne s4, t0, .Lbad
li t0, 1301
bne s5, t0, .Lbad
li t0, 1302
bne s6, t0, .Lbad
li t0, 1303
bne s7, t0, .Lbad
li t0, 1304
bne s8, t0, .Lbad
li t0, 1305
bne s9, t0, .Lbad
li t0, 1306
bne s10, t0, .Lbad
li t0, 1307
bne s11, t0, .Lbad
ld t0, 104(sp)
beqz t0, .Lrestore
ld t0, -112(sp)
bnez t0, .Lbad
ld t0, -104(sp)
bnez t0, .Lbad
ld t0, -96(sp)
bnez t0, .Lbad
ld t0, -88(sp)
bnez t0, .Lbad
ld t0, -80(sp)
bnez t0, .Lbad
ld t0, -72(sp)
bnez t0, .Lbad
ld t0, -64(sp)
bnez t0, .Lbad
ld t0, -56(sp)
bnez t0, .Lbad
j .Lrestore
.Lbad:
li a0, 1
.Lrestore:
ld ra, 96(sp)
ld s0, 0(sp)
ld s1, 8(sp)
ld s2, 16(sp)
ld s3, 24(sp)
ld s4, 32(sp)
ld s5, 40(sp)
ld s6, 48(sp)
ld s7, 56(sp)
ld s8, 64(sp)
ld s9, 72(sp)
ld s10, 80(sp)
ld s11, 88(sp)
addi sp, sp, 112
ret
.section .note.GNU-stack,"",@progbits
