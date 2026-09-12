from pathlib import Path
import tempfile,subprocess,os,json
r=Path(__file__).resolve().parents[1];hook=r/'firmware/buildroot/board/overlay/usr/share/udhcpc/default.script.d/50-resolvconf'
with tempfile.TemporaryDirectory() as d:
 p=Path(d);h=p/'hook';h.write_text(hook.read_text().replace('/boot/resolv.conf',str(p/'static')))
 fake=p/'resolvconf';fake.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$CAPTURE"\ncat >> "$CAPTURE"\n');fake.chmod(0o755)
 env=dict(os.environ,PATH=d+':/usr/bin:/bin',CAPTURE=str(p/'calls'),interface='wlan0',dns='192.0.2.1 192.0.2.2',search='example.test',domain='ignored.test')
 subprocess.run(['sh',str(h),'bound'],env=env,check=True)
 assert (p/'calls').read_text()=='-a wlan0.udhcpc\nsearch example.test\nnameserver 192.0.2.1\nnameserver 192.0.2.2\n'
 subprocess.run(['sh',str(h),'deconfig'],input='',env=env,text=True,check=True)
 assert (p/'calls').read_text().endswith('-d wlan0.udhcpc\n')
 before=(p/'calls').read_text();(p/'static').touch();subprocess.run(['sh',str(h),'renew'],env=env,check=True);assert (p/'calls').read_text()==before
print('DNS hook regression passed: lease publication, scoped withdrawal, static override')
