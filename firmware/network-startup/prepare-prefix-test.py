from pathlib import Path
import argparse, re, shutil, subprocess, tarfile
parser=argparse.ArgumentParser(description='Prepare a tiny root for the existing isolated DHCP diagnostic')
parser.add_argument('--workspace',type=Path,required=True)
parser.add_argument('--output',type=Path,required=True)
args=parser.parse_args()
base=args.workspace.resolve()
app=base/'app'; target=base/'enhanced/buildroot-release/target'
out=args.output.resolve()
assert not out.exists()
root=out/'root'
for d in ['usr/bin','usr/sbin','usr/lib','proc','tmp/nettest','run','dev','etc','boot','etc/kvm','kvmapp/kvm']:
    (root/d).mkdir(parents=True,exist_ok=True)
for name,dest in [('bin','usr/bin'),('sbin','usr/sbin'),('lib','usr/lib')]: (root/name).symlink_to(dest)
shutil.copy2(target/'usr/bin/busybox',root/'usr/bin/busybox')
shutil.copy2(target/'usr/sbin/ip',root/'usr/sbin/ip')
for name in ['sh','touch','mkdir','rm','ln','cat','printf','awk','sed','grep','tr','chmod','sleep','mv','cmp','flock','sha512sum','cut','kill','killall','readlink','ifconfig','udhcpc','udhcpd']:
    (root/'usr/bin'/name).symlink_to('busybox')
todo=[target/'usr/bin/busybox',target/'usr/sbin/ip']
done=set()
while todo:
    elf=todo.pop()
    text=subprocess.check_output(['readelf','-d',str(elf)],text=True)
    for name in re.findall(r'\(NEEDED\).*?\[(.*?)\]',text):
        if name in done:continue
        done.add(name)
        source=target/'usr/lib'/name
        shutil.copyfile(source,root/'usr/lib'/name)
        todo.append(source)
shutil.copyfile(target/'usr/lib/libc.so',root/'usr/lib/ld-musl-riscv64.so.1')
(root/'usr/lib/ld-musl-riscv64.so.1').chmod(0o755)
(root/'etc/profile').write_text('export PATH=/usr/sbin:/usr/bin:/sbin:/bin\n')
for source,name in [(app/'firmware/network-startup/runtime.sh','runtime.sh'),
                    (app/'firmware/buildroot/board/enhanced/init.d/S30rndis','S30rndis'),
                    (app/'kvmapp/system/init.d/S30wifi','S30wifi')]:
    shutil.copy2(source,root/'tmp/nettest'/name)
    (root/'tmp/nettest'/name).chmod(0o755)
# Exercise Windows-written input in the full daemon lifecycle too.
p=root/'tmp/nettest/runtime.sh'
s=p.read_text().replace("printf '10.200.201\\n'", "printf '10.200.201\\r\\n'")
p.write_text(s)
cross=base/'enhanced/buildroot-output/host/bin/riscv64-buildroot-linux-musl-gcc'
subprocess.run([str(cross),'-static','-Os','-mtune=thead-c906','-mno-fence-tso',str(app/'firmware/network-startup/netns-run.c'),'-o',str(out/'netns-run')],check=True)
with tarfile.open(out/'test.tar.gz','w:gz') as archive:
    archive.add(root,arcname='root')
    archive.add(out/'netns-run',arcname='netns-run')
print('Isolated diagnostic archive:',out/'test.tar.gz')
