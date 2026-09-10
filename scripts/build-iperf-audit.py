"""Build pinned temporary iperf3 tools on the Enhanced workspace host."""
from pathlib import Path
import hashlib, os, subprocess, tarfile, urllib.request, json
root = Path(__file__).resolve().parents[2]
out = root/'enhanced/iperf-audit'
out.mkdir(exist_ok=True)
archive = out/'iperf-3.21.tar.gz'
hashfile = root/'enhanced/sources/buildroot-2026.08-release/package/iperf3/iperf3.hash'
expected = next(line.split()[1] for line in hashfile.read_text().splitlines() if line.startswith('sha256') and line.endswith(archive.name))
if not archive.exists():
    urllib.request.urlretrieve('https://sources.buildroot.net/iperf3/'+archive.name, archive)
assert hashlib.sha256(archive.read_bytes()).hexdigest() == expected
source = out/'iperf-3.21'
if not source.exists():
    with tarfile.open(archive) as tar:
        tar.extractall(out, filter='data')
cross = str(root/'enhanced/buildroot-output/host/bin/riscv64-buildroot-linux-musl-')
for name in ['native', 'riscv64']:
    build = out/name
    build.mkdir(exist_ok=True)
    env = dict(os.environ, PATH='/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin')
    options = ['--disable-shared', '--enable-static', '--without-openssl']
    if name == 'riscv64':
        env.update(CC=cross+'gcc', AR=cross+'ar', RANLIB=cross+'ranlib',
                   CFLAGS='-Os -march=rv64gc -mtune=thead-c906 -mno-fence-tso -mabi=lp64d')
        options += ['--host=riscv64-buildroot-linux-musl']
    with (out/(name+'-build.log')).open('w') as log:
        for cmd in [[str(source/'configure'), *options], ['make', '-j4']]:
            result = subprocess.run(cmd, cwd=build, env=env, stdout=log, stderr=subprocess.STDOUT)
            if result.returncode:
                print((out/(name+'-build.log')).read_text()[-5000:])
                raise SystemExit(result.returncode)
    print(name+' build PASS', flush=True)
subprocess.run([cross+'strip', '--strip-unneeded', str(out/'riscv64/src/iperf3')], check=True)
report = {'version':'3.21', 'source':'https://sources.buildroot.net/iperf3/iperf-3.21.tar.gz',
          'source_matches_buildroot_hash':True, 'native_and_riscv64_build':'PASS',
          'openssl':False, 'persistent_install':False,
          'target_binary_bytes':(out/'riscv64/src/iperf3').stat().st_size}
(out/'build-report.json').write_text(json.dumps(report, indent=2)+'\n')
print(json.dumps(report))
