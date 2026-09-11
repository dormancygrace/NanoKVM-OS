"""Apply the small resolver adapter to the pinned core, for both TUN and DCO."""
import sys
from pathlib import Path
p = Path(sys.argv[1]) / 'openvpn/tun/linux/client/tunnetlink.hpp'
s = p.read_text()
marker = '// fixme -- Handle pushed DNS servers'
if marker not in s:
    raise SystemExit('OpenVPN 3 DNS integration point changed; review required')
s = '#include "nkos-dns.hpp"\n' + s.replace(marker, 'NkosDNS::configure(pull.dns_options, create, destroy);')
p.write_text(s)

# Pinned upstream DCO stop must finish cleanup even if peer counters vanished.
import subprocess
patch = Path(__file__).parent / 'patches/0001-dco-cleanup-after-peer-removal.patch'
with patch.open('rb') as f:
    subprocess.run(['patch', '-d', sys.argv[1], '-p1', '--forward'], stdin=f, check=True)
