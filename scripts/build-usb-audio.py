#!/usr/bin/env python3
"""Build the USB audio helper from pinned tinyalsa and Opus sources.

platform/build.sh prepares the sources from platform/sources.lock. The helper is
written to OUTPUT/usb-audio-capture and its notices to OUTPUT/share/usb-audio.
If the kernel builds the UAC1 gadget as modules, they are built too.
"""
import argparse, hashlib, json, shutil, subprocess, tarfile
from pathlib import Path
from nanokvm_cpu_profile import flags as cpu_flags, record as record_cpu_profile
a=argparse.ArgumentParser(description=__doc__)
a.add_argument('--output',type=Path,required=True)
a.add_argument('--buildroot-output',type=Path,required=True)
a.add_argument('--kernel-source',type=Path,required=True)
a.add_argument('--kernel-output',type=Path,required=True)
a.add_argument('--tinyalsa',type=Path,required=True,help='tinyalsa source tree')
a.add_argument('--opus-archive',type=Path,required=True,help='opus-1.6.1.tar.gz')
a.add_argument('--jobs',default='4')
args=a.parse_args()
repo=Path(__file__).resolve().parents[1];out=args.output.resolve();out.mkdir(parents=True,exist_ok=True)
cross=str(args.buildroot_output.resolve()/'host/bin/riscv64-buildroot-linux-musl-')
tiny=args.tinyalsa.resolve();revision='961babfe962e71d952ae734074cc89576b28a9e7'
expected='6ffcb593207be92584df15b32466ed64bbec99109f007c82205f0194572411a1'
if hashlib.sha256(args.opus_archive.read_bytes()).hexdigest()!=expected:raise SystemExit('Opus archive hash mismatch')
opus=out/'opus-1.6.1'
# Opus keeps __FILE__ in its assertions; map the build paths away.
maps=' '.join(f'-ffile-prefix-map={src}={name}' for src,name in [(out,'./usb-audio'),(tiny,'./tinyalsa'),(repo,'./nanokvm-os')])
if opus.exists():shutil.rmtree(opus)
with tarfile.open(args.opus_archive) as tar:tar.extractall(out,filter='data')
opus_build=out/'opus-build'
if opus_build.exists():shutil.rmtree(opus_build)
opus_build.mkdir()
with (out/'opus-build.log').open('w') as log:
    subprocess.run([str(opus/'configure'),'--host=riscv64-buildroot-linux-musl','--disable-shared','--enable-static','--disable-doc','--disable-extra-programs','--disable-fixed-point','--enable-float-api','CC='+cross+'gcc','CFLAGS='+' '.join(cpu_flags('audio'))+' '+maps],cwd=opus_build,stdout=log,stderr=subprocess.STDOUT,check=True)
    subprocess.run(['make','-j'+args.jobs],cwd=opus_build,stdout=log,stderr=subprocess.STDOUT,check=True)
record_cpu_profile(out/'cpu-profile.json', cross+'gcc', kind='audio')
kernel_config=(args.kernel_output/'.config').read_text()
audio_builtin='CONFIG_USB_U_AUDIO=y\n' in kernel_config and 'CONFIG_USB_F_UAC1=y\n' in kernel_config
module_artifacts=[]
if not audio_builtin:
    module=out/'module';module.mkdir(exist_ok=True)
    for name in ('u_audio.c','u_audio.h','f_uac1.c','u_uac1.h','uac_common.h'):
        shutil.copyfile(args.kernel_source/'drivers/usb/gadget/function'/name,module/name)
    # The platform kernel source already contains the composite IAD change.
    if 'audio_iad_desc' not in (module/'f_uac1.c').read_text():
        raise SystemExit('Use the kernel source prepared by platform/build.sh')
    (module/'Makefile').write_text('obj-m += u_audio.o usb_f_uac1.o\nusb_f_uac1-y := f_uac1.o\n')
    flags=' '.join(cpu_flags('kernel'))
    with (out/'module-build.log').open('w') as log:
        subprocess.run(['make','-C',str(args.kernel_source),'O='+str(args.kernel_output),'ARCH=riscv','CROSS_COMPILE='+cross,'CC='+cross+'gcc','KCFLAGS='+flags,'M='+str(module),'modules','-j'+args.jobs],stdout=log,stderr=subprocess.STDOUT,check=True)
    module_artifacts=[module/'u_audio.ko',module/'usb_f_uac1.ko']
helper=out/'usb-audio-capture'
cmd=[cross+'gcc',*cpu_flags('audio'),'-Wall','-Wextra','-Werror','-static',*maps.split(),'-I'+str(tiny/'include'),'-I'+str(tiny/'src'),'-I'+str(opus/'include'),str(repo/'native/usb-audio/capture.c')]+[str(tiny/'src'/f) for f in ('pcm.c','pcm_hw.c','limits.c','snd_card_plugin.c')]+[str(opus_build/'.libs/libopus.a'),'-lm','-o',str(helper)]
with (out/'helper-build.log').open('w') as log:subprocess.run(cmd,cwd=out,stdout=log,stderr=subprocess.STDOUT,check=True)
subprocess.run([cross+'strip',str(helper)],check=True)
license_dir=out/'share/usb-audio';license_dir.mkdir(parents=True,exist_ok=True)
shutil.copyfile(tiny/'LICENSE',license_dir/'tinyalsa-LICENSE');shutil.copyfile(opus/'COPYING',license_dir/'opus-COPYING')
(license_dir/'SOURCE').write_text('tinyalsa: https://github.com/tinyalsa/tinyalsa\nCommit: '+revision+'\nOpus: https://downloads.xiph.org/releases/opus/opus-1.6.1.tar.gz\nSHA256: '+expected+'\nFloating point, NanoKVM C906 CPU profile, 192000 bit/s, complexity 3, stereo 48 kHz, 20 ms, constrained VBR.\n')
manifest={str(f.relative_to(out)):hashlib.sha256(f.read_bytes()).hexdigest() for f in [helper,*module_artifacts]}
(out/'native-manifest.json').write_text(json.dumps(manifest,indent=2));print(json.dumps(manifest,indent=2))
