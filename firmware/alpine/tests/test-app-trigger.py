from pathlib import Path
import os, subprocess, tempfile
source=(Path(__file__).resolve().parents[1]/'packages/nanokvm-app/nanokvm-app.trigger').read_text()
with tempfile.TemporaryDirectory() as d:
    root=Path(d); live=root/'softlevel'; log=root/'calls'
    trigger=root/'trigger'; trigger.write_text(source.replace('/run/openrc/softlevel',str(live)))
    mock=root/'rc-service'
    mock.write_text('''#!/bin/sh
echo "$*" >> "$CALL_LOG"
case "$2" in status) exit "$STATUS_RESULT";; restart) exit "$RESTART_RESULT";; *) exit 99;; esac
'''); mock.chmod(0o755)
    env=dict(os.environ,PATH=d+':/usr/bin:/bin',CALL_LOG=str(log),STATUS_RESULT='0',RESTART_RESULT='0')
    def run(): return subprocess.run(['sh',str(trigger)],env=env,capture_output=True).returncode
    assert run()==0 and not log.exists(), 'offline image must not call OpenRC'
    live.touch(); env['STATUS_RESULT']='1'
    assert run()==0 and log.read_text().splitlines()==['nanokvm-app status'], 'stopped service must remain stopped'
    log.unlink(); env['STATUS_RESULT']='0'
    assert run()==0 and log.read_text().splitlines()==['nanokvm-app status','nanokvm-app restart'], 'live service must restart once'
    env['RESTART_RESULT']='7'
    assert run()==7, 'restart failure must reach APK'
print('PASS: offline, stopped, running and restart-failure cases')
