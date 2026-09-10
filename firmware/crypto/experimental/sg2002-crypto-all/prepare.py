#!/usr/bin/env python3
"""Extend the pinned streaming AES owner; prepare only, never load or submit DMA."""
from pathlib import Path
import argparse
import hashlib
import json
import shutil
import subprocess
import sys

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--repo', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
repo, out = a.repo.resolve(), a.output.resolve()
src = repo/'firmware/crypto/experimental/sg2002-crypto-all'
subprocess.run([sys.executable, str(repo/'firmware/crypto/experimental/sg2002-aes-probe/prepare-streaming-descriptor.py'),
                '--repo', str(repo), '--output', str(out)], check=True)
target = out/'sg2002_aes_probe.c'
text = target.read_text()
base_sha = hashlib.sha256(target.read_bytes()).hexdigest()

def replace(old, new):
    global text
    if text.count(old) != 1:
        raise RuntimeError('Unexpected pinned source shape: '+old)
    text = text.replace(old, new)

replace('static long aes_ioctl(', '#include "crypto_algorithms.inc"\n#include "crypto_linux_api.inc"\n\nstatic long aes_ioctl(')
replace(' if (cmd != SG2002_AES_CTR) return -ENOTTY;',
        ' if (cmd != SG2002_AES_CTR) return crypto_ioctl(cmd, arg);')
replace(' err = misc_register(&misc);\n if (err) goto data;',
        ' err = crypto_alloc_output();\n if (err) goto data;\n err = crypto_linux_register();\n if (err) goto crypto_data;\n err = misc_register(&misc);\n if (err) goto crypto_linux;')
replace('\ndata:\n', '\ncrypto_linux:\n crypto_linux_unregister();\ncrypto_data:\n crypto_free_output();\ndata:\n')
replace(' misc_deregister(&misc);', ' misc_deregister(&misc);\n crypto_linux_unregister();\n crypto_free_output();')
replace('sg2002_aes_probe: ready descriptor=streaming', 'sg2002_aes_probe: ready crypto-api=1 descriptor=streaming')
text = text.replace('; no DMA submitted', '; Linux Crypto API registered')
target.write_text(text)
for name in ('sg2002_crypto.h', 'crypto_algorithms.inc', 'crypto_linux_api.inc'):
    shutil.copyfile(src/name, out/name)
manifest = {
    'status': 'prepared, not built/installed/qualified',
    'base_streaming_sha256': base_sha,
    'files': {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted(out.iterdir()) if f.suffix in ('.c','.h','.inc')},
    'legacy_aes_descriptor_and_ioctl_unchanged': True,
    'shared_dma_owner': True,
    'software_fallback': False,
}
(out/'crypto-prepare.json').write_text(json.dumps(manifest, indent=2)+'\n')
print('Prepared CryptoDMA extension:', out)
