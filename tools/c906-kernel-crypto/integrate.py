#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Generate an opt-in kernel patch after the usercopy/checksum patch is applied."""
import argparse
import difflib
from pathlib import Path

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('kernel', type=Path)
    p.add_argument('patch', type=Path)
    p.add_argument('--chacha-threshold', type=int, default=2048)
    p.add_argument('--crc-scalar-threshold', type=int, default=1024)
    p.add_argument('--crc-vector-threshold', type=int, default=8192)
    p.add_argument('--copy-threshold', type=int, default=4096)
    a = p.parse_args()
    if min(a.chacha_threshold,a.crc_scalar_threshold,a.crc_vector_threshold,a.copy_threshold) < 1:
        raise ValueError('positive thresholds required')
    root = a.kernel.resolve()
    here = Path(__file__).resolve().parent
    changes = {}
    def edit(name, transform):
        old = (root/name).read_text() if (root/name).exists() else ''
        new = transform(old)
        changes[name] = (old,new)
    def replace(old, marker, replacement):
        if old.count(marker) != 1:
            raise ValueError(f'kernel source marker changed: {marker}')
        return old.replace(marker,replacement)
    configs = '''
config RISCV_ISA_XTHEADVECTOR_CHACHA
	bool "Experimental T-Head vector ChaCha library"
	depends on 64BIT && RISCV_ISA_V && RISCV_ISA_XTHEADVECTOR && MMU
	depends on CRYPTO_LIB_CHACHA && !KMSAN
	default n
	help
	  Explicit legacy RVV 0.7.1 ChaCha20/12 backend. This covers the
	  library path used by WireGuard. Short calls and unsuitable SIMD
	  contexts retain the existing backend. Compiler vectorization is
	  not enabled. HChaCha and Poly1305 keep their existing backends.

config RISCV_ISA_XTHEADVECTOR_CRC
	bool "Experimental T-Head vector and slicing-by-8 CRC"
	depends on 64BIT && RISCV_ISA_V && RISCV_ISA_XTHEADVECTOR && MMU
	depends on CRC32 && CRC32_ARCH
	default n
	help
	  CRC32 and CRC32C use scalar slicing-by-8 for medium buffers and
	  legacy vector lanes for large buffers. Early boot, short buffers
	  and CPUs without T-Head vectors retain their existing backends.

config RISCV_ISA_XTHEADVECTOR_COPY_CSUM
	bool "Experimental T-Head vector RAM copy and checksum"
	depends on 64BIT && RISCV_ISA_V && RISCV_ISA_XTHEADVECTOR && MMU
	default n
	help
	  Fuse csum_partial_copy_nocheck() for large kernel RAM buffers.
	  Faulting userspace copies keep their existing implementations.

'''
    edit('arch/riscv/Kconfig',lambda t:replace(t,'config RISCV_ISA_V_PREEMPTIVE',configs+'config RISCV_ISA_V_PREEMPTIVE'))
    asm = (here/'candidates.S').read_text()
    prefix = asm[:asm.index('/* VLEN128')]
    chacha = asm[asm.index('/* VLEN128'):asm.index('SYM_FUNC_START(kc_crc_lanes)')]
    crc = asm[asm.index('SYM_FUNC_START(kc_crc_lanes)'):asm.index('SYM_FUNC_START(kc_copy_csum)')]
    copy = asm[asm.index('SYM_FUNC_START(kc_copy_csum)'):asm.rindex('\t.option pop')]
    for name,body in [('lib/crypto/riscv/chacha-xtheadvector.S',chacha),
                      ('lib/crc/riscv/crc32-xtheadvector.S',crc),
                      ('arch/riscv/lib/copy_csum_xtheadvector.S',copy)]:
        edit(name,lambda t,body=body:prefix+body.replace('kc_','__riscv_th_')+'\t.option pop\n')
    edit('lib/crypto/Makefile',lambda t:t+'\nlibchacha-$(CONFIG_RISCV_ISA_XTHEADVECTOR_CHACHA) += riscv/chacha-xtheadvector.o\n')
    # Accumulate both changes in one file before constructing its diff.
    old_chacha = (root/'lib/crypto/chacha.c').read_text()
    new_chacha = replace(old_chacha,'void chacha_crypt(struct chacha_state *state, u8 *dst, const u8 *src,',
        '#ifdef CONFIG_RISCV_ISA_XTHEADVECTOR_CHACHA\n#include "riscv/chacha-xtheadvector.h"\n#endif\n\nvoid chacha_crypt(struct chacha_state *state, u8 *dst, const u8 *src,')
    new_chacha = replace(new_chacha,'\tchacha_crypt_arch(state, dst, src, bytes, nrounds);',
        '#ifdef CONFIG_RISCV_ISA_XTHEADVECTOR_CHACHA\n\tif (chacha_crypt_xtheadvector(state, dst, src, bytes, nrounds))\n\t\treturn;\n#endif\n\tchacha_crypt_arch(state, dst, src, bytes, nrounds);')
    changes['lib/crypto/chacha.c'] = (old_chacha,new_chacha)
    chacha_header = '''/* SPDX-License-Identifier: GPL-2.0-only */
#include <asm/simd.h>
#include <asm/vector.h>

void __riscv_th_chacha_blocks(struct chacha_state *, u8 *, size_t, int);
void __riscv_th_xor_bytes(u8 *, const u8 *, const u8 *, size_t);

static bool chacha_crypt_xtheadvector(struct chacha_state *state, u8 *dst,
		const u8 *src, unsigned int bytes, int rounds)
{
	u32 stream[64] __aligned(16);
	if (bytes < THRESHOLD || !has_xtheadvector() ||
	    riscv_vector_vlen() < 128 || !may_use_simd())
		return false;
	kernel_vector_begin();
	while (bytes >= 64) {
		unsigned int blocks = min(bytes / 64, 4U), n = blocks * 64;
		__riscv_th_chacha_blocks(state, (u8 *)stream, blocks, rounds);
		__riscv_th_xor_bytes(dst, src, (u8 *)stream, n);
		dst += n; src += n; bytes -= n;
	}
	if (bytes) {
		__riscv_th_chacha_blocks(state, (u8 *)stream, 1, rounds);
		__riscv_th_xor_bytes(dst, src, (u8 *)stream, bytes);
	}
	kernel_vector_end();
	memzero_explicit(stream, sizeof(stream));
	return true;
}
'''.replace('THRESHOLD',str(a.chacha_threshold))
    edit('lib/crypto/riscv/chacha-xtheadvector.h',lambda t:chacha_header)
    crc_c = (here/'crc.c').read_text().replace('#include "candidates.h"','#include <linux/init.h>\n#include <linux/cache.h>\n#include <asm/simd.h>\n#include <asm/vector.h>')
    crc_c = crc_c.replace('kc_','__riscv_th_').replace('static struct __riscv_th_crc_tables tables[2];',
        'static struct __riscv_th_crc_tables tables[2] __ro_after_init;\nbool __riscv_th_crc_ready __ro_after_init;')
    declarations = '''
void __riscv_th_tables_init(void);
u32 __riscv_th_crc_scalar(u32, const u8 *, size_t, bool);
u32 __riscv_th_crc_vector(u32, const u8 *, size_t, bool);
u32 __riscv_th_crc32(u32, const u8 *, size_t, bool);
'''
    crc_c = crc_c.replace('struct __riscv_th_crc_tables {',declarations+'\nstruct __riscv_th_crc_tables {')
    crc_c = crc_c.replace('void __riscv_th_tables_init(void)','void __init __riscv_th_tables_init(void)')
    marker = '\n}\nu32 __riscv_th_crc_scalar'
    crc_c = replace(crc_c,marker,'\n\t__riscv_th_crc_ready = true;\n}\nu32 __riscv_th_crc_scalar')
    crc_c += '''
u32 __riscv_th_crc32(u32 crc, const u8 *p, size_t n, bool kind)
{
	if (n >= VECTOR_THRESHOLD && riscv_vector_vlen() >= 128 && may_use_simd()) {
		kernel_vector_begin();
		crc = __riscv_th_crc_vector(crc, p, n, kind);
		kernel_vector_end();
		return crc;
	}
	return __riscv_th_crc_scalar(crc, p, n, kind);
}
'''.replace('VECTOR_THRESHOLD',str(a.crc_vector_threshold))
    # The vector routine's scalar-tail call is defined above its use.
    edit('lib/crc/riscv/crc32-xtheadvector.c',lambda t:crc_c)
    edit('lib/crc/Makefile',lambda t:t+'\ncrc32-$(CONFIG_RISCV_ISA_XTHEADVECTOR_CRC) += riscv/crc32-xtheadvector.o riscv/crc32-xtheadvector-asm.o\n')
    # Distinct C and assembler object names are required by kbuild.
    changes['lib/crc/riscv/crc32-xtheadvector-asm.S'] = changes.pop('lib/crc/riscv/crc32-xtheadvector.S')
    def crc_header(t):
        addition = '''
#ifdef CONFIG_RISCV_ISA_XTHEADVECTOR_CRC
#include <asm/vector.h>
extern bool __riscv_th_crc_ready;
void __riscv_th_tables_init(void);
u32 __riscv_th_crc32(u32, const u8 *, size_t, bool);
#define crc32_mod_init_arch __riscv_th_tables_init
#endif
'''
        t = replace(t,'#include "crc-clmul.h"','#include "crc-clmul.h"\n'+addition)
        for name,kind in [('crc32_le_arch','false'),('crc32c_arch','true')]:
            marker = f'static inline u32 {name}(u32 crc, const u8 *p, size_t len)\n{{'
            dispatch = f'''
#ifdef CONFIG_RISCV_ISA_XTHEADVECTOR_CRC
	if (__riscv_th_crc_ready && len >= {a.crc_scalar_threshold} && has_xtheadvector())
		return __riscv_th_crc32(crc, p, len, {kind});
#endif'''
            t = replace(t,marker,marker+dispatch)
        return t
    edit('lib/crc/riscv/crc32.h',crc_header)
    edit('arch/riscv/lib/Makefile',lambda t:t+'\nlib-$(CONFIG_RISCV_ISA_XTHEADVECTOR_COPY_CSUM) += copy_csum_xtheadvector.o\n')
    copy_header = '''
#ifdef CONFIG_RISCV_ISA_XTHEADVECTOR_COPY_CSUM
#define _HAVE_ARCH_CSUM_AND_COPY
__wsum csum_partial_copy_nocheck(const void *src, void *dst, int len);
#endif
'''
    edit('arch/riscv/include/asm/checksum.h',lambda t:replace(t,'#define ip_fast_csum ip_fast_csum',copy_header+'\n#define ip_fast_csum ip_fast_csum'))
    copy_c = '''
#ifdef CONFIG_RISCV_ISA_XTHEADVECTOR_COPY_CSUM
/* Only kernel RAM. Each even-size chunk bounds the widening reduction. */
unsigned int __riscv_th_copy_csum(void *, const void *, size_t);
__wsum csum_partial_copy_nocheck(const void *src, void *dst, int len)
{
	const u8 *p = src;
	u8 *q = dst;
	u32 sum = 0;
	if (len < COPY_THRESHOLD || !has_xtheadvector() ||
	    riscv_vector_vlen() < 128 || !may_use_simd()) {
		memcpy(dst, src, len);
		return csum_partial(dst, len, 0);
	}
	kasan_check_read(src, len);
	kasan_check_write(dst, len);
	kernel_vector_begin();
	while (len) {
		size_t n = min_t(size_t, len, 65536);
		sum += __riscv_th_copy_csum(q, p, n);
		p += n; q += n; len -= n;
	}
	kernel_vector_end();
	sum = (sum & 0xffff) + (sum >> 16);
	sum = (sum & 0xffff) + (sum >> 16);
	return (__force __wsum)sum;
}
EXPORT_SYMBOL(csum_partial_copy_nocheck);
#endif
'''.replace('COPY_THRESHOLD',str(a.copy_threshold))
    def copy_body(t):
        t = replace(t,'#include <asm/cpufeature.h>','#include <asm/cpufeature.h>\n#ifdef CONFIG_RISCV_ISA_XTHEADVECTOR_COPY_CSUM\n#include <asm/simd.h>\n#include <asm/vector.h>\n#include <linux/export.h>\n#include <net/checksum.h>\n#endif')
        return t+copy_c
    edit('arch/riscv/lib/csum.c',copy_body)
    patch = ''
    for name,(old,new) in changes.items():
        if new == old:
            raise ValueError(f'no change: {name}')
        patch += ''.join(difflib.unified_diff(old.splitlines(keepends=True),new.splitlines(keepends=True),
            fromfile='a/'+name,tofile='b/'+name))
    a.patch.write_text(patch)
    for name,(_,new) in changes.items():
        (root/name).parent.mkdir(parents=True,exist_ok=True)
        (root/name).write_text(new)
    print(f'Generated {len(changes)} opt-in kernel changes: {a.patch}')

if __name__ == '__main__':
    main()
