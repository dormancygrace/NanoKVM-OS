#!/usr/bin/env python3
"""Check monitor timing and filtering before programming receiver flash."""
import importlib.util
from pathlib import Path
import sys
import unittest

sys.dont_write_bytecode = True
HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('monitor_edids', HERE / 'build-monitor-edids.py')
generator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(generator)
ORIGINAL = (HERE.parent / 'tools/nanokvm_update_edid/E21_NanoKVM.bin').read_bytes()


class MonitorEdids(unittest.TestCase):
    def test_preferred_timing_and_bios_fallbacks(self):
        self.assertEqual({m[1] for m in generator.MODES}, {600, 720, 1080, 1440})
        for mode in generator.MODES:
            with self.subTest(height=mode[1]):
                data = generator.profile(ORIGINAL, mode)
                self.assertEqual(len(data), 256)
                self.assertEqual([sum(data[i:i+128]) % 256 for i in (0, 128)], [0, 0])
                self.assertEqual(data[8:18], ORIGINAL[8:18])
                self.assertEqual(data[35:38], ORIGINAL[35:38])
                self.assertEqual(data[38:54], ORIGINAL[38:54])
                d = data[54:72]
                width = d[2] | ((d[4] & 0xf0) << 4)
                height = d[5] | ((d[7] & 0xf0) << 4)
                hblank = d[3] | ((d[4] & 0x0f) << 8)
                vblank = d[6] | ((d[7] & 0x0f) << 8)
                hz = int.from_bytes(d[:2], 'little') * 10000 / ((width + hblank) * (height + vblank))
                self.assertEqual((width, height), mode[:2])
                self.assertAlmostEqual(hz, 30 if height == 1440 else 60, delta=0.35)
                # No second detailed timing or range permitting inferred modes.
                for pos in (72, 90, 108):
                    self.assertEqual(data[pos:pos+3], bytes(3))
                    self.assertIn(data[pos+3], (0x10, 0xfc, 0xff))
                pos, end = 132, 128 + data[130]
                while pos < end:
                    tag, length = data[pos] >> 5, data[pos] & 31
                    self.assertIn(tag, (1, 3, 4))
                    pos += length + 1
                self.assertEqual(pos, end)
                fallbacks = []
                while end + 18 <= 255 and data[end:end+2] != bytes(2):
                    dtd = data[end:end+18]
                    fallbacks.append((dtd[2] | ((dtd[4] & 0xf0) << 4), dtd[5] | ((dtd[7] & 0xf0) << 4)))
                    end += 18
                self.assertEqual(fallbacks, [m[:2] for m in generator.MODES if m[1] < height])
                self.assertEqual(data[end:255], bytes(255-end))
                if height == 1080:
                    self.assertEqual(d, ORIGINAL[54:72])

    def test_refuses_corrupt_input(self):
        damaged = bytearray(ORIGINAL)
        damaged[50] ^= 1
        with self.assertRaises(ValueError):
            generator.profile(damaged, generator.MODES[0])


if __name__ == '__main__':
    unittest.main()
