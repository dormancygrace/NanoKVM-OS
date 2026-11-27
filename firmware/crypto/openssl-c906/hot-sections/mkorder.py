#!/usr/bin/env python3
"""mkorder.py OUT BUILD READELF: a GNU ld --section-ordering-file for libcrypto and libssl from the call-count profiles
in this directory ("calls symbol" lines) and the function sections of a normal build (BUILD: the OpenSSL build tree
of scripts/build-openssl.py, READELF: the cross readelf).

The C906 has a 2-way 32 KiB instruction cache: the code one workload touches should be contiguous and small. Order:
  1. functions hot in more than one workload class (EVP, parameters, provider checks),
  2. ChaCha20-Poly1305 records: assembly kernels, then the functions only this class calls, with the TLS record path
     of the server (one connection sending tens of MB),
  3. AES-GCM records: GHASH assembly, their functions, then the AES assembly with its tables,
  4. SHA-256 and HMAC-SHA-256: their functions, then the SHA-256 assembly; then the other hashes with the SHA-512 and
     Keccak assembly,
  5. TLS connections: the server's handshakes (X25519MLKEM768, ECDSA P-256 signatures), s_server handshakes, ML-KEM.
The instrumented build counts functions the normal build inlines; only names with a section of their own in the
normal build count, and the share thresholds are taken from the most called of those. A static name with sections in
several NIST curve files is listed for P-256 only, the one curve the device uses.
Hot means: records at least 20000 calls (chacha.txt, gcm.txt: encryption; chachad.txt, gcmd.txt: decryption);
hashes at least 5% of the most called function of a profile (hash[0-9]*.txt); server records (srec.txt) and
connections (tls*.txt, shs.txt, kem.txt) at least 0.5%."""
import collections
import glob
import os
import subprocess
import sys

here = os.path.dirname(os.path.abspath(__file__))
out_path, build, readelf = sys.argv[1:4]

# Objects of the two shared libraries: their own code and the providers built into libcrypto.
SHARED_OBJECTS = ('libcrypto-shlib-', 'libssl-shlib-', 'libdefault-lib-', 'libcommon-lib-')
sections = collections.defaultdict(set)  # function name -> objects that have .text.<name>
for root, _, files in os.walk(build):
    for f in files:
        if f.endswith('.o') and f.startswith(SHARED_OBJECTS):
            text = subprocess.run([readelf, '-SW', os.path.join(root, f)], check=True, capture_output=True,
                                  text=True).stdout
            for word in text.split():
                if word.startswith('.text.') and not word.startswith('.text.unlikely'):
                    sections[word[6:]].add(f[:-2])
if not sections:
    raise SystemExit('no function sections under %s' % build)


def load(path):
    rows = {}
    for line in open(path):
        c, n = line.split()
        if n in sections:
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


chacha = hot(['chacha.txt', 'chachad.txt'], absolute=20000)
chacha.update({n: v for n, v in hot(['srec.txt'], share=0.005).items() if n not in chacha})
gcm = hot(['gcm.txt', 'gcmd.txt'], absolute=20000)
sha256 = hot(['hash1.txt', 'hash2.txt', 'hash7.txt', 'hash8.txt'], share=0.05)
hashes = hot(['hash%d.txt' % i for i in (3, 4, 5, 6, 9, 10, 11, 12, 13, 14)], share=0.05)
tls = hot(['tls*.txt', 'shs.txt', 'kem.txt'], share=0.005)
classes = [chacha, gcm, sha256, hashes]
shared = {n: max(g.get(n, 0) for g in classes) for n in set().union(*classes) if sum(n in g for g in classes) > 1}


def obj(*names):
    return ['  *libcrypto-shlib-%s.o(.text)\n' % n for n in names]


def entry(n):
    objs = sections[n]
    curves = sorted(o for o in objs if o.startswith('libcrypto-shlib-ecp_nistp'))
    if len(curves) > 1:
        return ['  *libcrypto-shlib-ecp_nistp256.o(.text.%s)\n' % n] if 'libcrypto-shlib-ecp_nistp256' in curves else []
    return ['  *(.text.%s)\n' % n]


seen = set()


def funcs(g):
    out = []
    for n in sorted(g, key=lambda n: (-g[n], n)):
        if n not in seen:
            seen.add(n)
            out += entry(n)
    return out


parts = [funcs(shared),
         obj('chacha-c906', 'chacha-scalar', 'poly1305-c906', 'poly1305-riscv') + funcs(chacha),
         obj('ghash-c906') + funcs(gcm) + obj('aes-riscv64'),
         funcs(sha256) + obj('sha256-c906'),
         funcs(hashes) + obj('sha512-c906', 'keccak1600-c906'),
         funcs(tls)]
with open(out_path, 'w') as f:
    f.write('.text : {\n')
    for title, p in zip(('shared', 'chacha20-poly1305', 'aes-gcm', 'sha256', 'hashes', 'tls'), parts):
        f.write('  /* %s */\n' % title)
        f.writelines(p)
    f.write('}\n')
print(out_path, [len(p) for p in parts], 'entries per part')
