#!/usr/bin/env python3
"""Generate isolated namespaced scalar LZ4 candidates from a supplied kernel."""
from pathlib import Path
import re

ALIGNED_WILD_COPY = """
    if (!(((uintptr_t)d | (uintptr_t)s) & 7)) {
        do {
            *(U64 *)d = *(const U64 *)s;
            d += 8; s += 8;
        } while (d < e);
        return;
    }
"""

def generate(source, output):
    source=Path(source);output=Path(output);output.mkdir(parents=True,exist_ok=True)
    original=(source/'lib/lz4/lz4defs.h').read_text()
    api=set()
    for name in ['lz4_compress.c','lz4_decompress.c']:
        api.update(re.findall(r'EXPORT_SYMBOL(?:_GPL)?\((LZ4_\w+)\)',(source/'lib/lz4'/name).read_text()))
    # This global helper is not exported by the kernel.
    api.add('LZ4_resetStream')
    count='''
    /* Two bounded word comparisons per loop; byte-exact prefix semantics. */
    while ((size_t)(pInLimit - pIn) >= 2 * STEPSIZE) {
        size_t diff = LZ4_read_ARCH(pMatch) ^ LZ4_read_ARCH(pIn);
        if (diff) return (unsigned int)(pIn - pStart) + LZ4_NbCommonBytes(diff);
        pIn += STEPSIZE; pMatch += STEPSIZE;
        diff = LZ4_read_ARCH(pMatch) ^ LZ4_read_ARCH(pIn);
        if (diff) return (unsigned int)(pIn - pStart) + LZ4_NbCommonBytes(diff);
        pIn += STEPSIZE; pMatch += STEPSIZE;
    }
'''
    wild='''
    /* Preserve eight-byte forward-copy ordering for overlapping matches. */
    do {
        LZ4_copy8(d, s);
        d += 8; s += 8;
        if (d >= e) break;
        LZ4_copy8(d, s);
        d += 8; s += 8;
    } while (d < e);
'''
    for variant in range(1,11):
        directory=output/f'v{variant}';directory.mkdir(exist_ok=True)
        header=original
        if variant in [2,6]:
            old='return get_unaligned((const size_t *)ptr);'
            assert header.count(old)==1
            header=header.replace(old,'if (!((uintptr_t)ptr & (sizeof(size_t) - 1))) return *(const size_t *)ptr;\n\t'+old)
            old='U64 a = get_unaligned((const U64 *)src);'
            assert header.count(old)==1
            header=header.replace(old,'''if (!(((uintptr_t)src | (uintptr_t)dst) & 7)) {
        *(U64 *)dst = *(const U64 *)src;
        return;
    }
    U64 a = get_unaligned((const U64 *)src);''')
        if variant in [9,10]:
            # Dispatch once per wild-copy span; every subsequent address stays
            # aligned as both pointers advance by eight bytes. Forward stores
            # preserve the existing overlapping-match contract.
            marker='BYTE *const e = (BYTE *)dstEnd;'
            assert header.count(marker)==1
            header=header.replace(marker,marker+ALIGNED_WILD_COPY)
        if variant in [7,8]:
            old='U64 a = get_unaligned((const U64 *)src);'
            assert header.count(old)==1
            header=header.replace(old,"""U64 a;
    if (!((uintptr_t)src & 7)) a = *(const U64 *)src;
    else a = get_unaligned((const U64 *)src);""")
            old='put_unaligned(a, (U64 *)dst);'
            assert header.count(old)==1
            header=header.replace(old,"""if (!((uintptr_t)dst & 7)) *(U64 *)dst = a;
    else put_unaligned(a, (U64 *)dst);""")
        if variant in [3,5,6,8]:
            marker='const BYTE *const pStart = pIn;'
            assert header.count(marker)==1;header=header.replace(marker,marker+count)
        if variant in [4,5,6,8,10]:
            old='''do {
        LZ4_copy8(d, s);
        d += 8;
        s += 8;
    } while (d < e);'''
            # Compare without formatting differences from kernel releases.
            normalized=header.replace('\t','    ')
            assert normalized.count(old)==1
            header=normalized.replace(old,wild.strip())
        (directory/'lz4defs.h').write_text(header)
        prefix=''.join(f'#define {name} c906_v{variant}_{name}\n' for name in sorted(api))
        for name in ['lz4_compress.c','lz4_decompress.c']:
            body=(source/'lib/lz4'/name).read_text()
            body=re.sub(r'^EXPORT_SYMBOL(?:_GPL)?\(.*\);?\s*$','',body,flags=re.M)
            body=re.sub(r'^MODULE_(?:LICENSE|DESCRIPTION|AUTHOR)\(.*\);?\s*$','',body,flags=re.M)
            (directory/name).write_text(prefix+body)
    return sorted(api)
if __name__=='__main__':
    import argparse
    p=argparse.ArgumentParser();p.add_argument('kernel');p.add_argument('output');a=p.parse_args()
    print(generate(a.kernel,a.output))
