#!/usr/bin/env python3
"""A kernel oops restarts the device after 10 s; hangs and lockups are only logged."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]


def kernel_config():
    settings = {}
    for line in (ROOT / 'platform/kernel/config').read_text().splitlines():
        match = re.fullmatch(r'(CONFIG_\w+)=(.*)', line) or re.fullmatch(r'# (CONFIG_\w+) is (not set)', line)
        if match:
            settings[match[1]] = match[2]
    return settings


class PanicPolicyTests(unittest.TestCase):
    def test_an_oops_panics_and_a_panic_restarts(self):
        config = kernel_config()
        self.assertEqual(config['CONFIG_PANIC_ON_OOPS'], 'y')
        self.assertEqual(config['CONFIG_PANIC_TIMEOUT'], '10')

    def test_hangs_and_lockups_are_logged_without_a_panic(self):
        # A slow SD card under f2fs garbage collection must not restart the device.
        config = kernel_config()
        self.assertEqual(config['CONFIG_DETECT_HUNG_TASK'], 'y')
        self.assertEqual(config['CONFIG_BOOTPARAM_HUNG_TASK_PANIC'], '0')
        self.assertEqual(config['CONFIG_SOFTLOCKUP_DETECTOR'], 'y')
        self.assertEqual(config['CONFIG_BOOTPARAM_SOFTLOCKUP_PANIC'], '0')

    def test_the_runtime_timeout_overrides_alpines(self):
        # OpenRC applies /usr/lib/sysctl.d (Alpine: kernel.panic = 120) before /etc/sysctl.d.
        conf = ROOT / 'firmware/alpine/etc/sysctl.d/30-nanokvm-panic.conf'
        values = dict(re.findall(r'^([\w.]+) = (\S+)$', conf.read_text(), re.M))
        self.assertEqual(values, {'kernel.panic': '10'})
        packages = (ROOT / 'platform/packages.list').read_text()
        self.assertRegex(packages, r'(?m)^base +file +/etc/sysctl\.d/30-nanokvm-panic\.conf +0644 +'
                                   r'firmware/alpine/etc/sysctl\.d/30-nanokvm-panic\.conf$')


if __name__ == '__main__':
    unittest.main()
