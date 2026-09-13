#!/usr/bin/env python3
"""Remove stale files owned only by packages deselected from the base image.

Buildroot does not uninstall on defconfig changes. Use its ownership manifest,
and preserve paths co-owned by a selected package. This runs only on host staging.
"""
import argparse,json,os,re,subprocess
from pathlib import Path

OPTIONAL = {'openvpn','wireless_tools','mc','nkos-superfile','nano','htop','tcpdump','ethtool','bluez5_utils','bluez5_utils-headers','dbus','libcap-ng','libffi','libglib2','libpcap','pcre2'}
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('target',type=Path);p.add_argument('--build-dir',type=Path,required=True);p.add_argument('--config',type=Path,required=True);p.add_argument('--report',type=Path)
 a=p.parse_args();target=a.target.resolve()
 if str(target)=='/':p.error('Refuse host root')
 cfg=set(a.config.read_text().splitlines())
 removed={name for name in OPTIONAL if 'BR2_PACKAGE_'+name.upper().replace('-','_')+'=y' not in cfg}
 ownership={}
 for line in (a.build_dir/'packages-file-list.txt').read_text().splitlines():
  owner,rel=line.split(',',1);rel=rel.removeprefix('./')
  if Path(rel).is_absolute() or '..' in Path(rel).parts:raise ValueError('Unsafe package file path')
  ownership.setdefault(rel,set()).add(owner)
 # Reconfiguring a package replaces its ownership list, while old feature
 # outputs can still remain in TARGET_DIR. Include the known Avahi feature
 # files even when they disappeared from the newly generated manifest.
 avahi_patterns=[]
 if 'dbus' in removed:
  avahi_patterns += ['usr/lib/libavahi-client.*', 'usr/share/dbus-1/interfaces/org.freedesktop.Avahi*']
  for name in ('avahi-browse','avahi-browse-domains','avahi-publish','avahi-publish-address','avahi-publish-service','avahi-resolve','avahi-resolve-address','avahi-resolve-host-name','avahi-set-host-name'):
   avahi_patterns.append('usr/bin/'+name)
 if 'libglib2' in removed:
  avahi_patterns += ['usr/lib/libavahi-glib.*', 'usr/lib/libavahi-gobject.*']
 for pattern in avahi_patterns:
  for path in target.glob(pattern):
   ownership.setdefault(str(path.relative_to(target)), {'avahi'})
 paths=[]
 for rel,owners in ownership.items():
  stale_avahi = False
  if owners == {'avahi'}:
   if 'dbus' in removed:
    stale_avahi = rel.startswith('usr/lib/libavahi-client.') or rel in {'usr/bin/avahi-browse','usr/bin/avahi-browse-domains','usr/bin/avahi-publish','usr/bin/avahi-publish-address','usr/bin/avahi-publish-service','usr/bin/avahi-resolve','usr/bin/avahi-resolve-address','usr/bin/avahi-resolve-host-name','usr/bin/avahi-set-host-name'} or '/dbus-1/' in rel
   if 'libglib2' in removed:
    stale_avahi |= rel.startswith(('usr/lib/libavahi-glib.','usr/lib/libavahi-gobject.'))
  stale_dbus_policy = owners == {'dnsmasq'} and 'dbus' in removed and '/dbus-1/' in rel
  if not owners or not (owners <= removed or stale_avahi or stale_dbus_policy):continue
  path=target/rel
  # Check parent, not the symlink leaf. Target aliases are removed, never followed.
  if not path.parent.resolve().is_relative_to(target):raise ValueError('Package path escaped target: '+rel)
  if path.is_symlink() or path.is_file():path.unlink();paths.append(rel)
 # Renamed runtime executable can survive an incremental CMake installation.
 old=target/'usr/sbin/nkos-openvpn3'
 if (target/'usr/sbin/openvpn3').is_file() and old.exists():old.unlink();paths.append('usr/sbin/nkos-openvpn3')
 for path in sorted(target.rglob('*'),key=lambda x:len(x.parts),reverse=True):
  if path.is_dir() and not path.is_symlink() and any(path.as_posix().endswith('/'+x) for x in ('mc','nano','bluetooth','dbus-1','wireless_tools')):
   try:path.rmdir()
   except OSError:pass
 report={'removed_packages':sorted(removed),'removed_files':sorted(paths)}
 if a.report:a.report.write_text(json.dumps(report,indent=2)+'\n')
 print('Pruned',len(paths),'files from',len(removed),'deselected packages')
 if 'dbus' in removed:
  for rel in ('usr/sbin/avahi-daemon','usr/sbin/dnsmasq'):
   path=target/rel
   if path.is_file():
    dynamic=subprocess.check_output(['readelf','-d',str(path)],text=True)
    if re.search(r'\(NEEDED\).*\[libdbus-',dynamic):
     raise SystemExit(rel+': rebuild avahi/dnsmasq after disabling D-Bus before producing an image')
if __name__=='__main__':main()
