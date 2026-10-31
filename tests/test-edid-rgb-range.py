#!/usr/bin/env python3
"""Check the Video Capability Data Block added to the monitor profiles."""
import importlib.util
from pathlib import Path
import sys
import unittest

sys.dont_write_bytecode = True
HERE = Path(__file__).resolve().parent


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, HERE.parent / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


rgb = load('edid_rgb_range', 'scripts/edid-rgb-range.py')
monitor = load('monitor_edids', 'scripts/build-monitor-edids.py')
ORIGINAL = (HERE.parent / 'support/nanokvm_update_edid/E21_NanoKVM.bin').read_bytes()


def blocks(data):
    pos, end, found = 132, 128 + data[130], []
    while pos < end:
        length = data[pos] & 31
        found.append(bytes(data[pos:pos+1+length]))
        pos += 1 + length
    return found, pos


def timings(data):
    pos, found = 128 + data[130], []
    while pos + 18 <= 255 and data[pos:pos+2] != bytes(2):
        found.append(bytes(data[pos:pos+18]))
        pos += 18
    return found, pos


class RgbRange(unittest.TestCase):
    def profiles(self):
        yield 'stock', ORIGINAL
        for mode in monitor.MODES:
            yield f'monitor-{mode[1]}', monitor.profile(ORIGINAL, mode)

    def test_adds_selectable_rgb_range_and_keeps_the_rest(self):
        for name, source in self.profiles():
            with self.subTest(profile=name):
                data = rgb.with_rgb_range(source)
                self.assertEqual(len(data), 256)
                self.assertEqual([sum(data[i:i+128]) % 256 for i in (0, 128)], [0, 0])
                self.assertEqual(data[:128], source[:128])
                self.assertEqual(data[128:130], source[128:130])
                self.assertEqual(data[131], source[131])
                before, _ = blocks(source)
                after, end = blocks(data)
                self.assertEqual(end, 128 + data[130])
                self.assertEqual(after, before + [bytes([0xE2, 0x00, 0x4A])])
                # QS set, QY clear: the receiver still delivers limited YCbCr.
                self.assertEqual(after[-1][2] & 0xC0, 0x40)
                self.assertEqual(timings(data)[0], timings(source)[0])
                self.assertEqual(data[timings(data)[1]:255], bytes(255 - timings(data)[1]))

    def test_idempotent(self):
        once = rgb.with_rgb_range(ORIGINAL)
        self.assertEqual(rgb.with_rgb_range(once), once)

    def test_refuses_a_different_capability_block(self):
        data = bytearray(rgb.with_rgb_range(ORIGINAL))
        _, end = blocks(data)
        data[end - 1] = 0xC0
        data[255] = (-sum(data[128:255])) & 255
        with self.assertRaises(ValueError):
            rgb.with_rgb_range(bytes(data))

    def test_refuses_a_full_extension(self):
        data = bytearray(ORIGINAL)
        _, pos = timings(data)
        data[pos:pos+2] = b'\x01\x1d'  # one more detailed timing
        data[pos+2:255] = b'\x01' * (255 - pos - 2)
        data[255] = (-sum(data[128:255])) & 255
        with self.assertRaises(ValueError):
            rgb.with_rgb_range(bytes(data))

    def test_refuses_corrupt_input(self):
        damaged = bytearray(ORIGINAL)
        damaged[200] ^= 1
        with self.assertRaises(ValueError):
            rgb.with_rgb_range(bytes(damaged))


if __name__ == '__main__':
    unittest.main()
