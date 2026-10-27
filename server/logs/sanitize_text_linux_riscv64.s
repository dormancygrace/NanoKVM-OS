//go:build linux && riscv64 && !purego

// C906 XTheadVector / RVV 0.7.1 byte kernels, not RVV 1.0.
// Leaf calls make no allocation or external call and preserve Go's g register.
// Vector lengths are bounded by the remaining valid bytes. The kernel handles
// vector context switches and signals; callers must check logVectorAvailable.
// WORD encodings are independently checked by tests/manual/c906-log-rvv/check-encodings.py.

#include "textflag.h"
TEXT ·logNeedsM1(SB), NOSPLIT, $0-17
MOV s_base+0(FP), X10
MOV s_len+8(FP), X11
BEQZ X11, clean
MOV $32, X6
MOV $94, X7
loop:
WORD $0x0005f2d7 // th.vsetvli t0,a1,e8,m1,d1
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x0a834857 // th.vsub.vx v16,v8,t1
WORD $0x7b03c057 // th.vmsgtu.vx v0,v16,t2
WORD $0x028bbc57 // th.vadd.vi v24,v8,-9
WORD $0x7b80b0d7 // th.vmsgtu.vi v1,v24,1
WORD $0x6600a057 // th.vmand.mm v0,v0,v1
WORD $0x56002e57 // th.vmfirst.m t3,v0
BGEZ X28, dirty
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
clean:
MOVB X0, ret+16(FP)
RET
dirty:
MOV $1, X5
MOVB X5, ret+16(FP)
RET

TEXT ·logIndexM1(SB), NOSPLIT, $0-32
MOV s_base+0(FP), X10
MOV X10, X8
MOV s_len+8(FP), X11
MOVBU value+16(FP), X12
BEQZ X11, missing
loop:
WORD $0x0005f2d7 // th.vsetvli t0,a1,e8,m1,d1
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x62864057 // th.vmseq.vx v0,v8,a2
WORD $0x56002357 // th.vmfirst.m t1,v0
BGEZ X6, found
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
missing:
MOV $-1, X6
MOV X6, ret+24(FP)
RET
found:
ADD X6, X10
SUB X8, X10
MOV X10, ret+24(FP)
RET

TEXT ·logPairM1(SB), NOSPLIT, $0-40
MOV s_base+0(FP), X10
MOV X10, X8
MOV s_len+8(FP), X11
MOVBU first+16(FP), X12
MOVBU last+17(FP), X15
MOV distance+24(FP), X13
BGEU X13, X11, missing
SUB X13, X11
loop:
WORD $0x0005f2d7 // th.vsetvli t0,a1,e8,m1,d1
ADD X13, X10, X14
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x02077807 // th.vle.v v16,(a4)
WORD $0x62864057 // th.vmseq.vx v0,v8,a2
WORD $0x6307c0d7 // th.vmseq.vx v1,v16,a5
WORD $0x6600a057 // th.vmand.mm v0,v0,v1
WORD $0x56002357 // th.vmfirst.m t1,v0
BGEZ X6, found
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
missing:
MOV $-1, X6
MOV X6, ret+32(FP)
RET
found:
ADD X6, X10
SUB X8, X10
MOV X10, ret+32(FP)
RET

TEXT ·logNeedsM2(SB), NOSPLIT, $0-17
MOV s_base+0(FP), X10
MOV s_len+8(FP), X11
BEQZ X11, clean
MOV $32, X6
MOV $94, X7
loop:
WORD $0x0015f2d7 // th.vsetvli t0,a1,e8,m2,d1
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x0a834857 // th.vsub.vx v16,v8,t1
WORD $0x7b03c057 // th.vmsgtu.vx v0,v16,t2
WORD $0x028bbc57 // th.vadd.vi v24,v8,-9
WORD $0x7b80b0d7 // th.vmsgtu.vi v1,v24,1
WORD $0x6600a057 // th.vmand.mm v0,v0,v1
WORD $0x56002e57 // th.vmfirst.m t3,v0
BGEZ X28, dirty
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
clean:
MOVB X0, ret+16(FP)
RET
dirty:
MOV $1, X5
MOVB X5, ret+16(FP)
RET

TEXT ·logIndexM2(SB), NOSPLIT, $0-32
MOV s_base+0(FP), X10
MOV X10, X8
MOV s_len+8(FP), X11
MOVBU value+16(FP), X12
BEQZ X11, missing
loop:
WORD $0x0015f2d7 // th.vsetvli t0,a1,e8,m2,d1
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x62864057 // th.vmseq.vx v0,v8,a2
WORD $0x56002357 // th.vmfirst.m t1,v0
BGEZ X6, found
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
missing:
MOV $-1, X6
MOV X6, ret+24(FP)
RET
found:
ADD X6, X10
SUB X8, X10
MOV X10, ret+24(FP)
RET

TEXT ·logPairM2(SB), NOSPLIT, $0-40
MOV s_base+0(FP), X10
MOV X10, X8
MOV s_len+8(FP), X11
MOVBU first+16(FP), X12
MOVBU last+17(FP), X15
MOV distance+24(FP), X13
BGEU X13, X11, missing
SUB X13, X11
loop:
WORD $0x0015f2d7 // th.vsetvli t0,a1,e8,m2,d1
ADD X13, X10, X14
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x02077807 // th.vle.v v16,(a4)
WORD $0x62864057 // th.vmseq.vx v0,v8,a2
WORD $0x6307c0d7 // th.vmseq.vx v1,v16,a5
WORD $0x6600a057 // th.vmand.mm v0,v0,v1
WORD $0x56002357 // th.vmfirst.m t1,v0
BGEZ X6, found
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
missing:
MOV $-1, X6
MOV X6, ret+32(FP)
RET
found:
ADD X6, X10
SUB X8, X10
MOV X10, ret+32(FP)
RET

TEXT ·logNeedsM4(SB), NOSPLIT, $0-17
MOV s_base+0(FP), X10
MOV s_len+8(FP), X11
BEQZ X11, clean
MOV $32, X6
MOV $94, X7
loop:
WORD $0x0025f2d7 // th.vsetvli t0,a1,e8,m4,d1
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x0a834857 // th.vsub.vx v16,v8,t1
WORD $0x7b03c057 // th.vmsgtu.vx v0,v16,t2
WORD $0x028bbc57 // th.vadd.vi v24,v8,-9
WORD $0x7b80b0d7 // th.vmsgtu.vi v1,v24,1
WORD $0x6600a057 // th.vmand.mm v0,v0,v1
WORD $0x56002e57 // th.vmfirst.m t3,v0
BGEZ X28, dirty
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
clean:
MOVB X0, ret+16(FP)
RET
dirty:
MOV $1, X5
MOVB X5, ret+16(FP)
RET

TEXT ·logIndexM4(SB), NOSPLIT, $0-32
MOV s_base+0(FP), X10
MOV X10, X8
MOV s_len+8(FP), X11
MOVBU value+16(FP), X12
BEQZ X11, missing
loop:
WORD $0x0025f2d7 // th.vsetvli t0,a1,e8,m4,d1
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x62864057 // th.vmseq.vx v0,v8,a2
WORD $0x56002357 // th.vmfirst.m t1,v0
BGEZ X6, found
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
missing:
MOV $-1, X6
MOV X6, ret+24(FP)
RET
found:
ADD X6, X10
SUB X8, X10
MOV X10, ret+24(FP)
RET

TEXT ·logPairM4(SB), NOSPLIT, $0-40
MOV s_base+0(FP), X10
MOV X10, X8
MOV s_len+8(FP), X11
MOVBU first+16(FP), X12
MOVBU last+17(FP), X15
MOV distance+24(FP), X13
BGEU X13, X11, missing
SUB X13, X11
loop:
WORD $0x0025f2d7 // th.vsetvli t0,a1,e8,m4,d1
ADD X13, X10, X14
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x02077807 // th.vle.v v16,(a4)
WORD $0x62864057 // th.vmseq.vx v0,v8,a2
WORD $0x6307c0d7 // th.vmseq.vx v1,v16,a5
WORD $0x6600a057 // th.vmand.mm v0,v0,v1
WORD $0x56002357 // th.vmfirst.m t1,v0
BGEZ X6, found
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
missing:
MOV $-1, X6
MOV X6, ret+32(FP)
RET
found:
ADD X6, X10
SUB X8, X10
MOV X10, ret+32(FP)
RET

TEXT ·logNeedsM8(SB), NOSPLIT, $0-17
MOV s_base+0(FP), X10
MOV s_len+8(FP), X11
BEQZ X11, clean
MOV $32, X6
MOV $94, X7
loop:
WORD $0x0035f2d7 // th.vsetvli t0,a1,e8,m8,d1
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x0a834857 // th.vsub.vx v16,v8,t1
WORD $0x7b03c057 // th.vmsgtu.vx v0,v16,t2
WORD $0x028bbc57 // th.vadd.vi v24,v8,-9
WORD $0x7b80b0d7 // th.vmsgtu.vi v1,v24,1
WORD $0x6600a057 // th.vmand.mm v0,v0,v1
WORD $0x56002e57 // th.vmfirst.m t3,v0
BGEZ X28, dirty
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
clean:
MOVB X0, ret+16(FP)
RET
dirty:
MOV $1, X5
MOVB X5, ret+16(FP)
RET

TEXT ·logIndexM8(SB), NOSPLIT, $0-32
MOV s_base+0(FP), X10
MOV X10, X8
MOV s_len+8(FP), X11
MOVBU value+16(FP), X12
BEQZ X11, missing
loop:
WORD $0x0035f2d7 // th.vsetvli t0,a1,e8,m8,d1
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x62864057 // th.vmseq.vx v0,v8,a2
WORD $0x56002357 // th.vmfirst.m t1,v0
BGEZ X6, found
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
missing:
MOV $-1, X6
MOV X6, ret+24(FP)
RET
found:
ADD X6, X10
SUB X8, X10
MOV X10, ret+24(FP)
RET

TEXT ·logPairM8(SB), NOSPLIT, $0-40
MOV s_base+0(FP), X10
MOV X10, X8
MOV s_len+8(FP), X11
MOVBU first+16(FP), X12
MOVBU last+17(FP), X15
MOV distance+24(FP), X13
BGEU X13, X11, missing
SUB X13, X11
loop:
WORD $0x0035f2d7 // th.vsetvli t0,a1,e8,m8,d1
ADD X13, X10, X14
WORD $0x02057407 // th.vle.v v8,(a0)
WORD $0x02077807 // th.vle.v v16,(a4)
WORD $0x62864057 // th.vmseq.vx v0,v8,a2
WORD $0x6307c0d7 // th.vmseq.vx v1,v16,a5
WORD $0x6600a057 // th.vmand.mm v0,v0,v1
WORD $0x56002357 // th.vmfirst.m t1,v0
BGEZ X6, found
ADD X5, X10
SUB X5, X11
BNEZ X11, loop
missing:
MOV $-1, X6
MOV X6, ret+32(FP)
RET
found:
ADD X6, X10
SUB X8, X10
MOV X10, ret+32(FP)
RET
