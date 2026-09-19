#!/usr/bin/env python3
from pathlib import Path
import tempfile,subprocess,shutil,json
r=Path(__file__).resolve().parents[1]
s=(r/'firmware/alpine/recovery/init').read_text();func=s[s.index('restore_settings() {'):s.index('\nvalidate_root()')]
with tempfile.TemporaryDirectory() as temp:
 t=Path(temp);(t/'realroot/etc').mkdir(parents=True)
 shadow=t/'realroot/etc/shadow';shadow.write_text('root:factory:1:0:99999:7:::\ndaemon:*:1:0:99999:7:::\n');shadow.chmod(0o640)
 (t/'root-shadow.saved').write_text('root:userhash:2:0:99999:7:::\n')
 func=func.replace('/busybox',shutil.which('busybox')).replace('/realroot',str(t/'realroot')).replace('/root-shadow.saved',str(t/'root-shadow.saved'))
 subprocess.run(['sh','-c','fail() { exit 1; }; SETTINGS_BACKUP=/nonexistent; '+func+'\nrestore_settings'],check=True)
 assert shadow.read_text()=='root:userhash:2:0:99999:7:::\ndaemon:*:1:0:99999:7:::\n'
 assert shadow.stat().st_mode&0o777==0o640
 print('PASS: recovery preserves root password, Alpine service accounts and shadow permissions')
