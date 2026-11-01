#!/usr/bin/env python3
"""nanokvm-rescue starts SSH only when SSH is off and the server never listens."""
import fcntl
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]
SERVICE = ROOT / 'firmware/alpine/openrc/nanokvm-rescue'
APP = ROOT / 'firmware/alpine/openrc/nanokvm-app'
S50SSHD = ROOT / 'firmware/alpine/compat/S50sshd'

# pidof NanoKVM-Server: LISTENING says from which check the server runs
# (never when unset), EVERY that it runs on every n-th check only.
PIDOF = '''#!/bin/sh
n=$(cat "$COUNT" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > "$COUNT"
if [ -n "${EVERY:-}" ]; then [ $((n % EVERY)) -eq 0 ] || exit 1
else [ -n "${LISTENING:-}" ] && [ "$n" -ge "$LISTENING" ] || exit 1; fi
echo 100
'''
TCP_HEADER = '  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n'


def listen(port, inode):
    return (f'   0: 00000000:{port} 00000000:0000 0A 00000000:00000000 00:00000000 00000000'
            f'     0        0 {inode} 1 0000000000000000 100 0 0 10 0\n')


def alive(pid):
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    # A killed worker stays a zombie until its parent is gone; it may also
    # be gone between the two checks.
    try:
        return Path(f'/proc/{pid}/stat').read_text().split()[2] != 'Z'
    except (FileNotFoundError, ProcessLookupError):
        return False


def wait_dead(pid):
    deadline = time.time() + 3
    while alive(pid) and time.time() < deadline:
        time.sleep(0.1)
    return not alive(pid)


class RescueTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        self.calls = self.root / 'calls'
        # FAIL_<name> makes a stub fail. A program that got the lock's fd
        # (sshd would keep it) is a call of its own, which every test refuses.
        for name in ('rc-service', 'ssh-keygen', 'logger'):
            var = 'FAIL_' + name.replace('-', '_')
            f = self.bin / name
            f.write_text(f'#!/bin/sh\necho "{name} $*" >> "$CALLS"\n'
                         f'[ ! -e /proc/$$/fd/9 ] || echo "fd 9 open in {name}" >> "$CALLS"\n'
                         f'[ -z "${{{var}:-}}" ]\n')
            f.chmod(0o755)
        # With BARRIER set, `rc-service sshd start` stays in progress until
        # that file exists, as a slow sshd start would.
        (self.bin / 'rc-service').write_text(
            '#!/bin/sh\n'
            'if [ -n "${BARRIER:-}" ] && [ "$*" = "sshd start" ]; then\n'
            '    echo start-begin >> "$CALLS"\n'
            '    while [ ! -e "$BARRIER" ]; do sleep 0.05; done\n'
            '    echo start-completed >> "$CALLS"\n'
            '    exit 0\n'
            'fi\n'
            'echo "rc-service $*" >> "$CALLS"\n'
            '[ ! -e /proc/$$/fd/9 ] || echo "fd 9 open in rc-service" >> "$CALLS"\n'
            '[ -z "${FAIL_rc_service:-}" ]\n')
        (self.bin / 'pidof').write_text(PIDOF)
        (self.bin / 'pidof').chmod(0o755)
        # The server (pid 100) owns socket 4242, listening on port 80.
        proc = self.root / 'proc'
        (proc / '100/fd').mkdir(parents=True)
        (proc / '100/fd/3').symlink_to('socket:[4242]')
        (proc / 'net').mkdir()
        self.tcp = proc / 'net/tcp'
        self.tcp.write_text(TCP_HEADER + listen('0050', 4242))
        self.stop = self.root / 'ssh_stop'
        self.stop.touch()
        self.run_dir = self.root / 'run/nanokvm'
        self.marker = self.run_dir / 'ssh-rescue'

    def environment(self, **env):
        return {**os.environ, 'SERVICE': str(SERVICE), 'CALLS': str(self.calls),
                'COUNT': str(self.root / 'count'), 'PATH': f'{self.bin}:/usr/bin:/bin',
                'NANOKVM_APP_SERVICE': str(APP), 'NANOKVM_SSH_STOP': str(self.stop),
                'NANOKVM_SERVER_CONFIG': str(self.root / 'absent.yaml'),
                'NANOKVM_PROC_ROOT': str(self.root / 'proc'),
                'NANOKVM_RUN': str(self.run_dir), 'NANOKVM_RESCUE_WAIT': '5',
                'NANOKVM_KMSG': str(self.root / 'kmsg'), **env}

    @staticmethod
    def script(function, real_sleep=False):
        return ('ebegin() { :; }; eend() { return "$1"; }; '
                + ('' if real_sleep else 'sleep() { :; }; ')
                + '. "$SERVICE"; ' + function)

    def run_service(self, function, **env):
        # As under openrc-run, the service's name is in the command line.
        return subprocess.run(['sh', '-c', self.script(function), 'nanokvm-rescue'], capture_output=True,
                              text=True, env=self.environment(**env))

    def calls_made(self):
        return self.calls.read_text().splitlines() if self.calls.exists() else []

    def test_a_server_that_listens_needs_no_rescue(self):
        result = self.run_service('rescue', LISTENING='3', NANOKVM_RESCUE_WAIT='20')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls_made(), [])
        self.assertFalse(self.marker.exists())
        # Up from the third check, stable for five.
        self.assertEqual((self.root / 'count').read_text().strip(), '7')

    def test_the_https_port_counts_too(self):
        self.tcp.write_text(TCP_HEADER + listen('01BB', 4242))
        result = self.run_service('rescue', LISTENING='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls_made(), [])

    def test_a_server_that_keeps_restarting_gets_ssh(self):
        # Running on every third check only, as under a crash loop.
        result = self.run_service('rescue', EVERY='3', NANOKVM_RESCUE_WAIT='20')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('rc-service sshd start', self.calls_made())

    def test_a_server_that_never_runs_gets_ssh(self):
        result = self.run_service('rescue')
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls_made()
        self.assertTrue(calls[0].startswith('logger -p daemon.warning -t nanokvm-rescue'), calls)
        self.assertEqual(calls[1:], ['ssh-keygen -A', 'rc-service sshd start'])
        self.assertTrue(self.marker.exists())
        self.assertIn('nanokvm-rescue: The NanoKVM server did not start', (self.root / 'kmsg').read_text())
        # Every second of the wait was a check.
        self.assertEqual((self.root / 'count').read_text().strip(), '5')

    def test_another_program_on_the_port_is_not_the_server(self):
        # The server runs, but the listener on port 80 is someone else's socket.
        self.tcp.write_text(TCP_HEADER + listen('0050', 9999))
        result = self.run_service('rescue', LISTENING='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('rc-service sshd start', self.calls_made())

    def test_the_server_on_another_port_is_not_up(self):
        self.tcp.write_text(TCP_HEADER + listen('1F90', 4242))
        result = self.run_service('rescue', LISTENING='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('rc-service sshd start', self.calls_made())

    def test_unknown_ports_accept_any_listener_of_the_server(self):
        config = self.root / 'server.yaml'
        config.write_text('port: {http: 80, https: 443}\n')
        self.tcp.write_text(TCP_HEADER + listen('1F90', 4242))
        result = self.run_service('rescue', LISTENING='1', NANOKVM_SERVER_CONFIG=str(config))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls_made(), [])

    def test_ssh_that_is_on_needs_no_rescue(self):
        self.stop.unlink()
        result = self.run_service('start; wait')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls_made(), [])
        self.assertFalse(self.marker.exists())

    def test_start_does_not_hold_the_boot(self):
        result = self.run_service('start; wait', NANOKVM_RESCUE_WAIT='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('rc-service sshd start', self.calls_made())

    def test_an_ssh_decision_during_the_wait_ends_the_rescue(self):
        for decision in ('decided', 'turned on'):
            with self.subTest(decision):
                self.calls.unlink(missing_ok=True)
                self.run_dir.mkdir(parents=True, exist_ok=True)
                if decision == 'decided':
                    (self.run_dir / 'ssh-decided').touch()
                else:
                    (self.run_dir / 'ssh-decided').unlink()
                    self.stop.unlink()
                result = self.run_service('rescue')
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.calls_made(), [])
                self.assertFalse(self.marker.exists())

    def test_a_decision_at_the_boundary_wins(self):
        # S50sshd holds the lock as the wait ends; the rescue rechecks under it.
        self.run_dir.mkdir(parents=True)
        with open(self.run_dir / 'ssh-rescue.lock', 'w') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            worker = subprocess.Popen(['sh', '-c', self.script('rescue')], env=self.environment(),
                                      stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            time.sleep(0.5)
            self.assertIsNone(worker.poll())
            (self.run_dir / 'ssh-decided').touch()
        self.assertEqual(worker.wait(5), 0)
        self.assertEqual(self.calls_made(), [])
        self.assertFalse(self.marker.exists())

    def test_s50sshd_decides_and_clears_the_marker(self):
        env = self.environment()
        for action, call in (('permanent_on', 'rc-service sshd start'),
                             ('permanent_off', 'rc-service sshd stop')):
            with self.subTest(action):
                self.run_dir.mkdir(parents=True, exist_ok=True)
                (self.run_dir / 'ssh-decided').unlink(missing_ok=True)
                self.marker.touch()
                result = subprocess.run(['sh', str(S50SSHD), action], capture_output=True, text=True, env=env)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertTrue((self.run_dir / 'ssh-decided').exists())
                self.assertFalse(self.marker.exists())
                self.assertEqual(self.calls_made()[-1], call)
                self.assertEqual(self.stop.exists(), action == 'permanent_off')

    def test_retiring_the_worker_waits_for_an_sshd_start_in_progress(self):
        # The old worker is starting sshd when the service stops (or
        # restarts) and the user then turns SSH off: the start must be over
        # before the choice is applied, so SSH ends up stopped.
        for retire, extra in (('stop', {}), ('start', {'LISTENING': '1', 'NANOKVM_RESCUE_WAIT': '20'})):
            with self.subTest(retire):
                barrier = self.root / f'barrier-{retire}'
                self.calls.unlink(missing_ok=True)
                (self.root / 'count').unlink(missing_ok=True)
                (self.run_dir / 'ssh-decided').unlink(missing_ok=True)
                env = self.environment(BARRIER=str(barrier), NANOKVM_RESCUE_WAIT='1')
                subprocess.run(['sh', '-c', self.script('start'), 'nanokvm-rescue'], check=True, env=env,
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                deadline = time.time() + 5
                while 'start-begin' not in self.calls_made() and time.time() < deadline:
                    time.sleep(0.05)
                self.assertIn('start-begin', self.calls_made())
                retiring = subprocess.Popen(['sh', '-c', self.script(retire), 'nanokvm-rescue'],
                                            env=self.environment(BARRIER=str(barrier), **extra),
                                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                turning_off = subprocess.Popen(['sh', str(S50SSHD), 'permanent_off'], env=env,
                                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                time.sleep(0.5)
                # Both wait for the start in progress.
                self.assertIsNone(retiring.poll())
                self.assertIsNone(turning_off.poll())
                barrier.touch()
                self.assertEqual(retiring.wait(5), 0)
                self.assertEqual(turning_off.wait(5), 0)
                calls = self.calls_made()
                self.assertEqual(calls[-1], 'rc-service sshd stop', calls)
                self.assertLess(calls.index('start-completed'), calls.index('rc-service sshd stop'), calls)
                self.assertTrue(self.stop.exists())
                self.assertTrue((self.run_dir / 'ssh-decided').exists())
                # A restarted worker found the server up and started nothing.
                if retire == 'start':
                    pid = int((self.run_dir / 'ssh-rescue.pid').read_text())
                    self.assertTrue(wait_dead(pid))
                    self.assertEqual(self.calls_made(), calls)

    def test_failures_are_logged_and_the_marker_needs_sshd(self):
        result = self.run_service('rescue', FAIL_ssh_keygen='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('rc-service sshd start', self.calls_made())
        self.assertIn('ssh-keygen -A failed', (self.root / 'kmsg').read_text())
        self.assertTrue(self.marker.exists())

        self.calls.unlink()
        result = self.run_service('rescue', FAIL_rc_service='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('SSH for repair did not start', (self.root / 'kmsg').read_text())
        # The marker of the earlier run is gone too.
        self.assertFalse(self.marker.exists())



if __name__ == '__main__':
    unittest.main()
