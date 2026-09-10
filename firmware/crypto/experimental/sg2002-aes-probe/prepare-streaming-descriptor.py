#!/usr/bin/env python3
"""Prepare an isolated cached-descriptor experiment; never install or submit DMA."""
from pathlib import Path
import argparse
import hashlib
import json
import shutil

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--repo', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
src = a.repo/'firmware/crypto/experimental/sg2002-aes-probe'
base = (src/'sg2002_aes_probe.c').read_text()
expected = '3334bd2d66135e842ed3a40e431cc7fb25a1e9091f051ea3b73bc0d4a94fcb2a'
if hashlib.sha256((src/'sg2002_aes_probe.c').read_bytes()).hexdigest() != expected:
    p.error('Source differs from the physically qualified request-scratch revision')

text = base
def replace_once(old, new):
    global text
    if text.count(old) != 1:
        raise ValueError('Unexpected source shape: '+old)
    text = text.replace(old, new)

replace_once(' dma_wmb();', ''' dma_sync_single_for_device(&pdev->dev, desc_dma, DESC_BYTES, DMA_BIDIRECTIONAL);
 dma_wmb();''')
replace_once(' dma_rmb();', ''' dma_rmb();
 dma_sync_single_for_cpu(&pdev->dev, desc_dma, DESC_BYTES, DMA_BIDIRECTIONAL);''')
replace_once(' desc = dma_alloc_coherent(&pdev->dev, DESC_BYTES, &desc_dma, GFP_KERNEL);\n if (!desc) {err = -ENOMEM; goto device;}', ''' /* Experimental: vendor-style cached descriptor with explicit DMA ownership.
  * Payload, descriptor bytes, register programming and timeout policy stay R8.
  * BIDIRECTIONAL conservatively allows device descriptor writeback.
  */
 desc = kzalloc(DESC_BYTES, GFP_KERNEL);
 if (!desc) {err = -ENOMEM; goto device;}
 desc_dma = dma_map_single(&pdev->dev, desc, DESC_BYTES, DMA_BIDIRECTIONAL);
 if (dma_mapping_error(&pdev->dev, desc_dma)) {err = -EIO; goto free_descriptor;}
 dma_sync_single_for_cpu(&pdev->dev, desc_dma, DESC_BYTES, DMA_BIDIRECTIONAL);''')
replace_once('descriptor:\n dma_free_coherent(&pdev->dev, DESC_BYTES, desc, desc_dma);', '''descriptor:
 dma_unmap_single(&pdev->dev, desc_dma, DESC_BYTES, DMA_BIDIRECTIONAL);
free_descriptor:
 kfree_sensitive(desc);''')
replace_once(' dma_free_coherent(&pdev->dev, DESC_BYTES, desc, desc_dma);', ''' dma_unmap_single(&pdev->dev, desc_dma, DESC_BYTES, DMA_BIDIRECTIONAL);
 kfree_sensitive(desc);''')
replace_once('sg2002_aes_probe: ready coherent=', 'sg2002_aes_probe: ready descriptor=streaming coherent=')
assert [s for s in text.splitlines() if 'writel(' in s] == [s for s in base.splitlines() if 'writel(' in s]
assert [s for s in text.splitlines() if 'desc[' in s] == [s for s in base.splitlines() if 'desc[' in s]

out = a.output.resolve()
out.mkdir(parents=True, exist_ok=False)
(out/'sg2002_aes_probe.c').write_text(text)
for name in ['sg2002_aes_probe.h', 'Makefile']:
    shutil.copyfile(src/name, out/name)
manifest = {
    'status': 'prepared experiment, not built or installed',
    'base_sha256': expected,
    'candidate_sha256': hashlib.sha256(text.encode()).hexdigest(),
    'change': 'cached streaming descriptor with explicit device/CPU synchronization',
    'descriptor_format_register_writes_payload_and_timeout_changed': False,
    'failure_retains_both_mappings': True,
    'dma_stability_proven': False,
}
(out/'prepare-manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
print(json.dumps(manifest, indent=2))
