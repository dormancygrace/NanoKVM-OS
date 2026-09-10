/* SPDX-License-Identifier: GPL-2.0-only */
.text
.option push
.option arch, +xtheadba
.balign 4
.global probe_ba
.type probe_ba,@function
probe_ba:
li a0,3
li a1,5
th.addsl a0,a0,a1,2
ret
.size probe_ba,.-probe_ba
.option pop
.option push
.option arch, +xtheadbb
.balign 4
.global probe_bb
.type probe_bb,@function
probe_bb:
li a0,0x12345678
th.extu a0,a0,15,8
ret
.size probe_bb,.-probe_bb
.option pop
.option push
.option arch, +xtheadbs
.balign 4
.global probe_bs
.type probe_bs,@function
probe_bs:
li a0,128
th.tst a0,a0,7
ret
.size probe_bs,.-probe_bs
.option pop
.option push
.option arch, +xtheadcondmov
.balign 4
.global probe_condmov
.type probe_condmov,@function
probe_condmov:
li a0,3
li a1,11
li a2,1
th.mvnez a0,a1,a2
ret
.size probe_condmov,.-probe_condmov
.option pop
.option push
.option arch, +xtheadmac
.balign 4
.global probe_mac
.type probe_mac,@function
probe_mac:
li a0,3
li a1,5
li a2,2
th.mula a0,a1,a2
ret
.size probe_mac,.-probe_mac
.option pop
.option push
.option arch, +xtheadmemidx
.balign 4
.global probe_memidx
.type probe_memidx,@function
probe_memidx:
li a1,8
th.lrd a0,a0,a1,0
ret
.size probe_memidx,.-probe_memidx
.option pop
.option push
.option arch, +xtheadmempair
.balign 4
.global probe_mempair
.type probe_mempair,@function
probe_mempair:
mv a2,a0
th.ldd a0,a1,(a2),0,4
add a0,a0,a1
ret
.size probe_mempair,.-probe_mempair
.option pop
.option push
.option arch, +xtheadfmemidx
.balign 4
.global probe_fmemidx
.type probe_fmemidx,@function
probe_fmemidx:
li a1,8
th.flrd fa0,a0,a1,0
fmv.x.d a0,fa0
ret
.size probe_fmemidx,.-probe_fmemidx
.option pop
.option push
.option arch, +xtheadsync
.balign 4
.global probe_sync
.type probe_sync,@function
probe_sync:
th.sync
li a0,1
ret
.size probe_sync,.-probe_sync
.option pop
.option push
.option arch, +xtheadvector
.balign 4
.global probe_vector
.type probe_vector,@function
probe_vector:
li t0,16
th.vsetvli t0,t0,e8,m1
th.vmv.v.i v0,7
th.vadd.vi v0,v0,1
th.vsb.v v0,(a0)
mv a0,t0
ret
.size probe_vector,.-probe_vector
.option pop
.section .note.GNU-stack,"",@progbits
