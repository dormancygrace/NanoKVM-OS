#!/usr/bin/env python3
"""Prepare full-update images for signing; never upload or write a device."""
import argparse, gzip, hashlib, json, os, re, shutil, subprocess, tempfile
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__)
for name in ['rootfs','fit','ram-fit-directory','initramfs-list','gen-init-cpio','mkimage','fip','host-apk','output']:
    p.add_argument('--'+name,type=Path,required=True)
p.add_argument('--version',required=True)
p.add_argument('--sequence',type=int,required=True)
p.add_argument('--kernel-release',required=True)
p.add_argument('--ram-kernel-release',required=True)
p.add_argument('--minimum-updater',type=int,default=2)
p.add_argument('--updater-capability',type=int,required=True)
p.add_argument('--epoch',type=int,default=1789164000)
a=p.parse_args()
if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?',a.version) or a.sequence<1:
    p.error('invalid version/sequence')
if not re.fullmatch(r'[0-9][0-9A-Za-z._+-]{0,95}',a.kernel_release):p.error('invalid kernel release')
if not re.fullmatch(r'[0-9][0-9A-Za-z._+-]{0,95}',a.ram_kernel_release):p.error('invalid RAM kernel release')
if a.minimum_updater < 1 or a.updater_capability < 2 or a.minimum_updater > a.updater_capability:p.error('invalid updater capability transition')
if a.output.exists():p.error('use a fresh output directory')
if a.rootfs.stat().st_size!=1610612736:p.error('unsupported root partition size')
def read_image(path):
    return subprocess.check_output(['debugfs','-R','cat '+path,str(a.rootfs)],stderr=subprocess.DEVNULL,text=True).strip()
if read_image('/kvmapp/version')!=a.version:p.error('rootfs version mismatch')
installed=json.loads(read_image('/kvmapp/.os-update/installed.json'))
if installed.get('sequence')!=a.sequence or installed.get('version')!=a.version:p.error('rootfs release sequence mismatch')
updater=json.loads(read_image('/kvmapp/.os-update/updater.json'))
if updater.get('capability')!=a.updater_capability:p.error('rootfs updater capability mismatch')
updater_sequence=updater.get('sequence')
updater_id=updater.get('id','')
if not isinstance(updater_sequence,int) or updater_sequence < 0 or (updater_sequence == 0 and updater_id) or (updater_sequence > 0 and not re.fullmatch(r'[a-f0-9]{64}',updater_id)):p.error('invalid rootfs updater monotonic state')
with tempfile.TemporaryDirectory(prefix='nkos-contract-') as temporary:
    contract_path=Path(temporary)/'addons-contract.json'
    subprocess.run(['debugfs','-R','dump /usr/share/nkos/addons-contract.json '+str(contract_path),str(a.rootfs)],stderr=subprocess.DEVNULL,check=True)
    contract_raw=contract_path.read_bytes()
    # The RAM verifier handles empty-addon migration from legacy images and is
    # also the fallback verifier when a source image has no addon manager.
    manager_path=Path(temporary)/'nkos-addons'
    subprocess.run(['debugfs','-R','dump /usr/sbin/nkos-addons '+str(manager_path),str(a.rootfs)],stderr=subprocess.DEVNULL,check=True)
    manager_raw=manager_path.read_bytes()
    updater_path=Path(temporary)/'nkos-update'
    subprocess.run(['debugfs','-R','dump /usr/sbin/nkos-update '+str(updater_path),str(a.rootfs)],stderr=subprocess.DEVNULL,check=True)
    updater_raw=updater_path.read_bytes()
    target_updater_sha256=hashlib.sha256(updater_raw).hexdigest()
contract=json.loads(contract_raw)
for field in ['format','base_abi','server_api','features','trust','repositories']:
    if field not in contract:p.error('incomplete target addon contract: '+field)
if contract['format'] != 1 or not isinstance(contract['base_abi'],str) or not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+',contract['base_abi']) or not isinstance(contract['server_api'],int) or contract['server_api'] < 1:p.error('invalid target addon ABI contract')
if not isinstance(contract['features'],list) or contract['features'] != sorted(set(contract['features'])):p.error('addon feature provides must be a sorted unique list')
trust_json=json.dumps(contract['trust'],sort_keys=True)
if 'keys_dir' in trust_json or 'PUBLIC KEY' not in trust_json:p.error('addon trust must embed public key material, not a source path')
keys=contract['trust'].get('keys') if isinstance(contract['trust'],dict) else None
if not isinstance(keys,list) or not keys:p.error('target addon contract has no trust keys')
key_ids=set()
for key in keys:
    if not isinstance(key,dict) or not re.fullmatch(r'[a-z0-9][a-z0-9+_.-]*',str(key.get('id',''))) or not re.fullmatch(r'[a-z0-9][a-z0-9+_.-]*',str(key.get('filename',''))) or not str(key.get('public_key_pem','')).startswith('-----BEGIN PUBLIC KEY-----\n') or not str(key.get('public_key_pem','')).endswith('-----END PUBLIC KEY-----\n') or key['id'] in key_ids:p.error('invalid or duplicate target addon trust key')
    key_ids.add(key['id'])
if not isinstance(contract['repositories'],list) or not contract['repositories']:p.error('invalid target addon repositories')
for repository in contract['repositories']:
    if not isinstance(repository,dict) or not str(repository.get('url','')).startswith('https://') or not isinstance(repository.get('key_ids'),list) or not repository['key_ids'] or any(key_id not in key_ids for key_id in repository['key_ids']):p.error('invalid target addon repository trust binding')
expected_providers={'nkos-base-abi':contract['base_abi'],'nkos-server-api':str(contract['server_api'])}
for feature in contract['features']:
    if not isinstance(feature,str) or feature.count('=')!=1:p.error('invalid target addon feature')
    name,version=feature.split('=',1)
    if not re.fullmatch(r'nkos-feature-[a-z0-9][a-z0-9-]*',name) or not re.fullmatch(r'[0-9]+',version) or name in expected_providers:p.error('invalid target addon feature')
    expected_providers[name]=version
with tempfile.TemporaryDirectory(prefix='nkos-apk-image-') as temporary:
    apk_root=Path(temporary)
    (apk_root/'etc/apk/keys').mkdir(parents=True)
    (apk_root/'lib/apk/db').mkdir(parents=True)
    def dump_image(image_path,destination):
        subprocess.run(['debugfs','-R','dump '+image_path+' '+str(destination),str(a.rootfs)],stderr=subprocess.DEVNULL,check=True)
    dump_image('/opt/nkos/etc/apk/arch',apk_root/'etc/apk/arch')
    dump_image('/opt/nkos/etc/apk/world',apk_root/'etc/apk/world')
    dump_image('/opt/nkos/lib/apk/db/installed',apk_root/'lib/apk/db/installed')
    if (apk_root/'etc/apk/arch').read_text().strip()!='riscv64':p.error('target addon APK architecture mismatch')
    if sorted(line.strip() for line in (apk_root/'etc/apk/world').read_text().splitlines() if line.strip()) != sorted(name+'='+version for name,version in expected_providers.items()):p.error('target addon provider world mismatch')
    for key in keys:
        destination=apk_root/'etc/apk/keys'/key['filename']
        dump_image('/opt/nkos/etc/apk/keys/'+key['filename'],destination)
        if destination.read_text()!=key['public_key_pem']:p.error('target addon trust key differs from signed contract')
    if not a.host_apk.is_file() or not os.access(a.host_apk,os.X_OK):p.error('host apk executable is unavailable')
    query=subprocess.check_output([str(a.host_apk),'--root',str(apk_root),'--repositories-file','/dev/null','--keys-dir',str(apk_root/'etc/apk/keys'),'--no-network','query','--format','json','--installed','--fields','name,version,description,arch,contents','*'],text=True)
    installed=json.loads(query)
    if not isinstance(installed,list) or len(installed)!=len(expected_providers):p.error('target image contains unexpected addon APK state')
    seen_installed=set()
    for package in installed:
        if not isinstance(package,dict) or expected_providers.get(package.get('name'))!=package.get('version') or package.get('name') in seen_installed or package.get('arch')!='noarch' or package.get('description')!='virtual meta package' or package.get('contents') not in (None,[]):p.error('target image immutable provider database mismatch')
        seen_installed.add(package['name'])
    if seen_installed!=set(expected_providers):p.error('target image immutable provider inventory mismatch')
def sha(p):
    with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
out=a.output;out.mkdir(parents=True)
(out/'nkos-addons').write_bytes(manager_raw)
(out/'nkos-update').write_bytes(updater_raw)
def require_static_riscv(path,label):
    environment=dict(os.environ,LC_ALL='C')
    elf_header=subprocess.check_output(['readelf','-h',str(path)],text=True,env=environment)
    elf_program_headers=subprocess.check_output(['readelf','-l',str(path)],text=True,env=environment)
    if not re.search(r'Class:\s+ELF64',elf_header) or not re.search(r'Data:\s+2.s complement, little endian',elf_header) or not re.search(r'Type:\s+EXEC',elf_header) or not re.search(r'Machine:\s+RISC-V',elf_header) or 'INTERP' in elf_program_headers or 'DYNAMIC' in elf_program_headers:p.error(label+' must be a static RISC-V ELF64 executable')
require_static_riscv(out/'nkos-addons','addon manager')
require_static_riscv(out/'nkos-update','target updater')
(out/'addons-contract.json').write_bytes(contract_raw)
with a.rootfs.open('rb') as src,(out/'rootfs.ext4.gz').open('wb') as dst:
    with gzip.GzipFile(filename='',mode='wb',fileobj=dst,mtime=a.epoch,compresslevel=3) as gz:shutil.copyfileobj(src,gz,4*1024*1024)
shutil.copyfile(a.fit,out/'normal-boot.sd')
root,packed,fit,fip=sha(a.rootfs),sha(out/'rootfs.ext4.gz'),sha(out/'normal-boot.sd'),sha(a.fip)
addon_contract=sha(out/'addons-contract.json')
repo=Path(__file__).resolve().parents[1]
init=(repo/'firmware/update/full-init.sh.in').read_text()
values={'ROOT':root,'PACKED':packed,'FIT':fit,'FIP':fip,'ADDON_CONTRACT':addon_contract,'STAGE':packed,'KERNEL':a.kernel_release,'RAM_KERNEL':a.ram_kernel_release,'VERSION':a.version,'DISPLAY':'v'+a.version.replace('-beta.',' beta-')}
for key,value in values.items():init=init.replace('@'+key+'@',value)
if re.search(r'@[A-Z]+@',init):p.error('unresolved init template token')
(out/'init').write_text(init)
subprocess.run(['sh','-n',str(out/'init')],check=True)
listing=a.initramfs_list.read_text();lines=listing.splitlines()
found=False
for i,line in enumerate(lines):
    fields=line.split()
    if len(fields)>2 and fields[0]=='file' and fields[1]=='/init':
        fields[2]=str((out/'init').resolve());lines[i]=' '.join(fields);found=True
if not found:p.error('initramfs list has no /init')
(out/'initramfs.list').write_text('\n'.join(lines)+'\n')
subprocess.run([str(a.gen_init_cpio),'-t',str(a.epoch),'-o',str(out/'initramfs.cpio'),str(out/'initramfs.list')],check=True)
(out/'initramfs.cpio.gz').write_bytes(gzip.compress((out/'initramfs.cpio').read_bytes(),mtime=a.epoch))
for name in ['Image.zst','board.dtb']:shutil.copyfile(a.ram_fit_directory/name,out/name)
ram_kernel=subprocess.check_output(['zstd','-dc',str(out/'Image.zst')])
if b'Linux version '+a.ram_kernel_release.encode()+b' ' not in ram_kernel:p.error('RAM FIT kernel release mismatch')
its=(a.ram_fit_directory/'boot.its').read_text()
(out/'boot.its').write_text(its)
with (out/'mkimage.log').open('w') as log:
    subprocess.run([str(a.mkimage),'-f','boot.its','ram-update.sd'],cwd=out,env=dict(os.environ,SOURCE_DATE_EPOCH=str(a.epoch)),stdout=log,stderr=subprocess.STDOUT,check=True)
if (out/'ram-update.sd').stat().st_size>7900000:p.error('RAM installer exceeds qualified boot budget')
subprocess.run([str(a.mkimage.with_name('dumpimage')),'-T','flat_dt','-p','1','-o',str(out/'readback.cpio.gz'),str(out/'ram-update.sd')],stdout=subprocess.DEVNULL,check=True)
if (out/'readback.cpio.gz').read_bytes()!=(out/'initramfs.cpio.gz').read_bytes():raise RuntimeError('RAM disk FIT roundtrip mismatch')
meta={'platform':'sg2002-sd-v1','minimum_updater':a.minimum_updater,'target_updater':a.updater_capability,'target_updater_sequence':updater_sequence,'target_updater_sha256':target_updater_sha256,'addon_contract_sha256':addon_contract,'rootfs_bytes':a.rootfs.stat().st_size,'rootfs_sha256':root,'bootloader_sha256':fip,'kernel_release':a.kernel_release}
(out/'full-system.json').write_text(json.dumps(meta,indent=2)+'\n')
(out/'image-checks.json').write_text(json.dumps({'version':a.version,'sequence':a.sequence,'files':{n:sha(out/n) for n in ['rootfs.ext4.gz','normal-boot.sd','ram-update.sd','addons-contract.json','nkos-addons','nkos-update']},'ramdisk_roundtrip':True,'hardware_install_tested':False},indent=2)+'\n')
print(out/'full-system.json')
