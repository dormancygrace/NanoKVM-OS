#!/usr/bin/env python3
import hashlib,json,sys,subprocess
from pathlib import Path
root=Path(sys.argv[1]).resolve()
manifest=json.loads((root/'manifest.json').read_text())
assert manifest['kernel']=='7.2.5-nanokvm-os'
assert len(manifest['files'])>14
for entry in manifest['files']:
    p=(root/entry['path']).resolve()
    assert p.is_relative_to(root)
    assert hashlib.sha256(p.read_bytes()).hexdigest()==entry['sha256'], str(p)
print('Board asset hashes verified:',len(manifest['files']))
# The release string stayed constant across kernel configurations. Reject the
# old single-compressor module even if its own manifest hashes are consistent.
zram=root/'usr/lib/modules/7.2.5-nanokvm-os/kernel/drivers/block/zram/zram.ko'
symbols=subprocess.check_output(['readelf','-sW',str(zram)],text=True)
assert ' recompress_store' in symbols and ' recomp_algorithm_store' in symbols, 'Enhanced requires the multi-compressor ZRAM module'

# DCO is part of the release contract, not an optional developer extra.
ovpn=root/'usr/lib/modules/7.2.5-nanokvm-os/kernel/drivers/net/ovpn/ovpn.ko'
assert ovpn.is_file(), 'Enhanced requires the upstream OpenVPN DCO module'
assert any(entry['path'] == str(ovpn.relative_to(root)) for entry in manifest['files']), 'DCO module missing from verified manifest'

if manifest.get('beta_candidate'):
    for name in ('sg2002_aes_probe', 'cryptodev', 'aic8800_bsp', 'aic8800_fdrv', '8733bs'):
        path = root/('usr/lib/modules/7.2.5-nanokvm-os/extra/'+name+'.ko')
        assert path.is_file(), 'Missing beta module: '+name
        assert any(entry['path'] == str(path.relative_to(root)) for entry in manifest['files']), 'Beta module missing from manifest: '+name
