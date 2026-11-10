#!/usr/bin/env python3
"""mkorder.py OUT: a GNU ld --section-ordering-file for libcrypto and libssl from the call-count profiles in this directory
("calls symbol" lines).

The C906 has a 2-way 32 KiB instruction cache: the code one workload touches should be contiguous and small. Order:
  1. functions hot in more than one workload class (EVP, parameters, provider checks),
  2. ChaCha20-Poly1305 records: assembly kernels, then the functions only this class calls,
  3. AES-GCM records: GHASH assembly, their functions, then the AES assembly with its tables,
  4. SHA-256 and HMAC-SHA-256: their functions, then the SHA-256 assembly; then the other hashes,
  5. TLS connections (key exchange, signatures, certificate handling).
Hot means: records at least 20000 calls (chacha.txt, gcm.txt); hashes at least 5% of the most called function of a profile
(hash[0-9]*.txt); TLS at least 0.5% (tls*.txt)."""
import glob
import os
import sys

here = os.path.dirname(os.path.abspath(__file__))


def load(path):
    rows = {}
    for line in open(path):
        c, n = line.split()
        if not n.startswith('?'):
            rows[n] = rows.get(n, 0) + int(c)
    return rows


def hot(patterns, absolute=None, share=None):
    g = {}
    for pattern in patterns:
        for path in sorted(glob.glob(os.path.join(here, pattern))):
            rows = load(path)
            top = max(rows.values())
            for n, c in rows.items():
                if (absolute and c >= absolute) or (share and c >= top * share):
                    g[n] = max(g.get(n, 0.0), c / top)
    return g


chacha = hot(['chacha.txt'], absolute=20000)
gcm = hot(['gcm.txt'], absolute=20000)
sha256 = hot(['hash1.txt', 'hash2.txt', 'hash7.txt', 'hash8.txt'], share=0.05)
hashes = hot(['hash%d.txt' % i for i in (3, 4, 5, 6, 9, 10, 11, 12, 13, 14)], share=0.05)
tls = hot(['tls*.txt'], share=0.005)
classes = [chacha, gcm, sha256, hashes]
shared = {n: max(g.get(n, 0) for g in classes) for n in set().union(*classes) if sum(n in g for g in classes) > 1}


def obj(*names):
    return ['  *libcrypto-shlib-%s.o(.text)\n' % n for n in names]


seen = set()


def funcs(g):
    out = []
    for n in sorted(g, key=lambda n: (-g[n], n)):
        if n not in seen:
            seen.add(n)
            out.append('  *(.text.%s)\n' % n)
    return out


parts = [funcs(shared),
         obj('chacha-c906', 'chacha-scalar', 'poly1305-c906', 'poly1305-riscv') + funcs(chacha),
         obj('ghash-c906') + funcs(gcm) + obj('aes-riscv64'),
         funcs(sha256) + obj('sha256-c906'),
         funcs(hashes) + obj('sha512-c906'),
         funcs(tls)]
with open(sys.argv[1], 'w') as f:
    f.write('.text : {\n')
    for title, p in zip(('shared', 'chacha20-poly1305', 'aes-gcm', 'sha256', 'hashes', 'tls'), parts):
        f.write('  /* %s */\n' % title)
        f.writelines(p)
    f.write('}\n')
print(sys.argv[1], [len(p) for p in parts], 'entries per part')
