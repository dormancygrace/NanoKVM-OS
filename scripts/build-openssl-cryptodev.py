#!/usr/bin/env python3
"""Build OpenSSL 3.6.4 with scalar ASM and cryptodev; stage only, never deploy."""
import argparse,hashlib,json,os,shutil,subprocess,tarfile
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--archive',type=Path,required=True)
p.add_argument('--buildroot-output',type=Path,required=True)
p.add_argument('--output',type=Path,required=True)
p.add_argument('--jobs',type=int,default=8)
a=p.parse_args();repo=Path(__file__).resolve().parents[1];out=a.output.resolve()
expected='9bffaa1ad1e07b354c21bd3324ec02fa15579f45a7d0494b3e74bc449b7333ef'
if out.exists() or not 1<=a.jobs<=32:p.error('Choose a fresh output and 1..32 jobs')
if hashlib.sha256(a.archive.read_bytes()).hexdigest()!=expected:p.error('Expected the recorded official OpenSSL 3.6.4 archive')
out.mkdir(parents=True)
with tarfile.open(a.archive) as tf:tf.extractall(out,filter='data')
src=out/'openssl-3.6.4';build=out/'build';build.mkdir()
inc=out/'include/crypto';inc.mkdir(parents=True)
shutil.copyfile(repo/'firmware/crypto/cryptodev-linux/crypto/cryptodev.h',inc/'cryptodev.h')
cross=str(a.buildroot_output.resolve()/'host/bin/riscv64-buildroot-linux-musl-')
isa_flags=json.loads((Path(__file__).resolve().parents[1]/'firmware/toolchain/thead/profile.json').read_text())['userspace_flags']
env=dict(os.environ,CC=cross+'gcc',AR=cross+'ar',RANLIB=cross+'ranlib',PATH=str(a.buildroot_output.resolve()/'host/bin')+':/usr/bin:/bin',CFLAGS='-Os '+isa_flags,CPPFLAGS='-I'+str(out/'include'))
options=['linux64-riscv64','--prefix=/usr','--openssldir=/etc/ssl','--libdir=lib','threads','shared','enable-asm','enable-engine','enable-dynamic-engine','enable-devcryptoeng','no-afalgeng','no-rc5','enable-camellia','no-docs','no-tests','no-fuzz-libfuzzer','no-fuzz-afl','no-sm2-precomp','zlib-dynamic']
with (out/'build.log').open('w') as log:
 for command in (['perl',str(src/'Configure'),*options],['make','-j'+str(a.jobs)],['make','DESTDIR='+str(out/'stage'),'install_sw']):
  subprocess.run(command,cwd=build,env=env,stdout=log,stderr=subprocess.STDOUT,check=True)
files={str(f.relative_to(out/'stage')):hashlib.sha256(f.read_bytes()).hexdigest() for f in (out/'stage').rglob('*') if f.is_file() and not f.is_symlink()}
(out/'manifest.json').write_text(json.dumps({'status':'built-not-installed','openssl':'3.6.4','archive_sha256':expected,'configure':options,'CFLAGS':'-Os '+isa_flags,'files':files,'hardware_use_proven_by_build':False},indent=2)+'\n')
print('OpenSSL stage:',out/'stage')