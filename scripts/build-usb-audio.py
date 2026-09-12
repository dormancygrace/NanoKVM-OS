#!/usr/bin/env python3
"""Build the USB audio helper and modules without rebuilding or installing the kernel."""
import argparse,hashlib,json,os,shutil,subprocess,tarfile,urllib.request
from pathlib import Path
a=argparse.ArgumentParser(description=__doc__)
a.add_argument('--work',type=Path,required=True)
a.add_argument('--buildroot-output',type=Path,required=True)
a.add_argument('--kernel-source',type=Path,required=True)
a.add_argument('--kernel-output',type=Path,required=True)
args=a.parse_args()
repo=Path(__file__).resolve().parents[1];out=args.work.resolve();out.mkdir(parents=True,exist_ok=True)
cross=str(args.buildroot_output.resolve()/'host/bin/riscv64-buildroot-linux-musl-')
tiny=out/'tinyalsa';revision='9fab97ca07184371ecad81154d1dadb09d0fa7cf'
if not tiny.exists():
    subprocess.run(['git','clone','https://github.com/tinyalsa/tinyalsa.git',str(tiny)],check=True)
    subprocess.run(['git','-C',str(tiny),'checkout','--detach',revision],check=True)
if subprocess.check_output(['git','-C',str(tiny),'rev-parse','HEAD'],text=True).strip()!=revision:raise SystemExit('Unexpected tinyalsa revision')
if subprocess.check_output(['git','-C',str(tiny),'diff','--name-only'],text=True).strip():raise SystemExit('Modified tinyalsa sources')
archive=out/'opus-1.5.2.tar.gz';expected='65c1d2f78b9f2fb20082c38cbe47c951ad5839345876e46941612ee87f9a7ce1'
if not archive.exists():urllib.request.urlretrieve('https://downloads.xiph.org/releases/opus/opus-1.5.2.tar.gz',archive)
if hashlib.sha256(archive.read_bytes()).hexdigest()!=expected:raise SystemExit('Opus archive hash mismatch')
opus=out/'opus-1.5.2'
if not opus.exists():
    with tarfile.open(archive) as tar:tar.extractall(out,filter='data')
opus_build=out/'opus-build';opus_build.mkdir(exist_ok=True)
with (out/'opus-build.log').open('w') as log:
    subprocess.run([str(opus/'configure'),'--host=riscv64-buildroot-linux-musl','--disable-shared','--enable-static','--disable-doc','--disable-extra-programs','CC='+cross+'gcc','CFLAGS=-O3 -march=rv64gc -mtune=thead-c906 -mno-fence-tso'],cwd=opus_build,stdout=log,stderr=subprocess.STDOUT,check=True)
    subprocess.run(['make','-j4'],cwd=opus_build,stdout=log,stderr=subprocess.STDOUT,check=True)
module=out/'module';module.mkdir(exist_ok=True)
for name in ('u_audio.c','u_audio.h','f_uac1.c','u_uac1.h','uac_common.h'):
    shutil.copyfile(args.kernel_source/'drivers/usb/gadget/function'/name,module/name)
if 'audio_iad_desc' not in (module/'f_uac1.c').read_text():
    subprocess.run(['patch','-p5','-i',str(repo/'firmware/kernel/patches/0024-uac1-composite-iad.patch')],cwd=module,check=True)
(module/'Makefile').write_text('obj-m += u_audio.o usb_f_uac1.o\nusb_f_uac1-y := f_uac1.o\n')
flags='-march=rv64imac_zicsr_zifencei_zacas_zabha_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync -mtune=thead-c906 -mno-fence-tso -fno-tree-vectorize -fno-tree-slp-vectorize'
with (out/'module-build.log').open('w') as log:
    subprocess.run(['make','-C',str(args.kernel_source),'O='+str(args.kernel_output),'ARCH=riscv','CROSS_COMPILE='+cross,'KCFLAGS='+flags,'M='+str(module),'modules','-j4'],stdout=log,stderr=subprocess.STDOUT,check=True)
helper=repo/'kvmapp/system/bin/usb-audio-capture';helper.parent.mkdir(parents=True,exist_ok=True)
cmd=[cross+'gcc','-O2','-Wall','-Wextra','-Werror','-static','-march=rv64gc','-mtune=thead-c906','-mno-fence-tso','-I'+str(tiny/'include'),'-I'+str(tiny/'src'),'-I'+str(opus/'include'),str(repo/'native/usb-audio/capture.c')]+[str(tiny/'src'/f) for f in ('pcm.c','pcm_hw.c','limits.c','snd_card_plugin.c')]+[str(opus_build/'.libs/libopus.a'),'-lm','-o',str(helper)]
with (out/'helper-build.log').open('w') as log:subprocess.run(cmd,stdout=log,stderr=subprocess.STDOUT,check=True)
subprocess.run([cross+'strip',str(helper)],check=True)
license_dir=repo/'kvmapp/system/share/usb-audio';license_dir.mkdir(parents=True,exist_ok=True)
shutil.copyfile(tiny/'LICENSE',license_dir/'tinyalsa-LICENSE');shutil.copyfile(opus/'COPYING',license_dir/'opus-COPYING')
(license_dir/'SOURCE').write_text('tinyalsa: https://github.com/tinyalsa/tinyalsa\nCommit: '+revision+'\nOpus: https://downloads.xiph.org/releases/opus/opus-1.5.2.tar.gz\nSHA256: '+expected+'\nFloating point, scalar rv64gc, 192000 bit/s, complexity 3, stereo 48 kHz, 20 ms, constrained VBR.\n')
manifest={str(f.relative_to(out)) if f.is_relative_to(out) else str(f.relative_to(repo)):hashlib.sha256(f.read_bytes()).hexdigest() for f in (helper,module/'u_audio.ko',module/'usb_f_uac1.ko')}
(out/'native-manifest.json').write_text(json.dumps(manifest,indent=2));print(json.dumps(manifest,indent=2))
