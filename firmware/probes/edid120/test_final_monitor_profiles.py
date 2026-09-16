#!/usr/bin/env python3
"""Validate preferred high-rate EDIDs without dropping legacy fallback modes."""
import unittest
from pathlib import Path
import build_final_monitor_profiles as profiles


class MonitorProfilesTest(unittest.TestCase):
    def setUp(self):
        self.source = (Path(__file__).parent / "NanoKVM-final-qhd40-720p120-fhd75.bin").read_bytes()
        _, start = profiles.PRIMARY.parse_cta_blocks(self.source)
        self.original = {bytes(raw) for _, raw, _ in profiles.PRIMARY.parse_cta_dtds(self.source, start)}

    def test_preferred_modes_and_fallbacks(self):
        for height, (width, _, rate) in profiles.PREFERRED.items():
            with self.subTest(height=height):
                data, timing = profiles.build_profile(self.source, height)
                self.assertEqual(len(data), 256)
                self.assertEqual(sum(data[:128]) % 256, 0)
                self.assertEqual(sum(data[128:]) % 256, 0)
                self.assertEqual((timing.width, timing.height), (width, height))
                self.assertAlmostEqual(timing.refresh_hz, rate, delta=0.05)
                _, start = profiles.PRIMARY.parse_cta_blocks(data)
                modes = profiles.PRIMARY.parse_cta_dtds(data, start)
                self.assertTrue(self.original.issubset({bytes(raw) for _, raw, _ in modes}))
                self.assertEqual(sum(t.width == 2560 and t.height == 1440 and abs(t.refresh_hz - 50) < .05 for _, _, t in modes), 1)
                self.assertEqual(data[128:146], self.source[128:146])

    def test_unreviewed_input_is_rejected(self):
        changed = bytearray(self.source)
        changed[10] ^= 1
        with self.assertRaises(ValueError):
            profiles.build_profile(bytes(changed), 1440)


if __name__ == "__main__":
    unittest.main()
