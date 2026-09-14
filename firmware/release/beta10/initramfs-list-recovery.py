from pathlib import Path
import os
import argparse
p=argparse.ArgumentParser(description='Recover a cpio file list from an extracted normal initramfs')
p.add_argument('--output', type=Path, required=True)
out=p.parse_args().output.resolve()
root = out/'initramfs/root'
lines = []
for p in sorted(root.rglob('*')):
    relative = '/'+str(p.relative_to(root))
    mode = format(p.lstat().st_mode & 0o777,'o')
    if p.is_symlink(): lines.append(f'slink {relative} {os.readlink(p)} {mode} 0 0')
    elif p.is_dir(): lines.append(f'dir {relative} {mode} 0 0')
    elif p.is_file(): lines.append(f'file {relative} {p} {mode} 0 0')
lines += ['nod /dev/console 600 0 0 c 5 1','nod /dev/null 666 0 0 c 1 3']
(out/'initramfs/initramfs.list').write_text('\n'.join(lines)+'\n')
print('Reusable beta.9 initramfs file list generated')
