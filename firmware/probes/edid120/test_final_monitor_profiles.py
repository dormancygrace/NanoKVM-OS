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

    def test_uhd_profile(self):
        data, timing = profiles.build_uhd_profile(self.source)
        self.assertEqual(len(data), 256)
        self.assertEqual([sum(data[i:i + 128]) % 256 for i in (0, 128)], [0, 0])
        self.assertEqual((timing.width, timing.height), (3840, 2160))
        self.assertAlmostEqual(timing.refresh_hz, 30, delta=0.05)
        self.assertEqual(timing.pixel_clock_hz, 262_750_000)
        # Range limit admits the UHD30 clock; the HDMI block states 300 MHz.
        self.assertEqual(data[profiles.PRIMARY.find_range_descriptor(data) + 9], 27)
        blocks, start = profiles.PRIMARY.parse_cta_blocks(data)
        self.assertIn((3, bytes.fromhex("030c001000003c")), blocks)
        self.assertIn(4, dict(blocks)[2])  # 720p60 stays as CTA VIC 4.
        modes = profiles.PRIMARY.parse_cta_dtds(data, start)
        kept = {bytes(raw) for _, raw, _ in modes}
        # Every CTA mode of the QHD50 profile except the 720p60 DTD.
        qhd, _ = profiles.build_profile(self.source, 1440)
        _, qstart = profiles.PRIMARY.parse_cta_blocks(qhd)
        expected = {bytes(raw) for _, raw, t in profiles.PRIMARY.parse_cta_dtds(qhd, qstart)
                    if not (t.height == 720 and abs(t.refresh_hz - 60) < .05)}
        self.assertEqual(kept, expected)
        self.assertEqual(len(modes), len(expected))

    def test_rate_profiles(self):
        for height, rates in profiles.RATES.items():
            default = profiles.build_rate_profile(self.source, height, rates[0])[0]
            for rate in rates:
                with self.subTest(height=height, rate=rate):
                    data, timing = profiles.build_rate_profile(self.source, height, rate)
                    self.assertEqual([sum(data[i:i + 128]) % 256 for i in (0, 128)], [0, 0])
                    self.assertEqual((timing.width, timing.height), (profiles.WIDTHS[height], height))
                    self.assertAlmostEqual(timing.refresh_hz, rate, delta=0.1)
                    # The preferred timing changes and faster modes of this
                    # resolution are gone; everything else is the default.
                    self.assertEqual(data[:38], default[:38])
                    self.assertEqual(data[72:127], default[72:127])
                    _, start = profiles.PRIMARY.parse_cta_blocks(data)
                    modes = [t for _, _, t in profiles.PRIMARY.parse_cta_dtds(data, start)]
                    self.assertFalse([t for t in modes if (t.width, t.height) == (timing.width, height)
                                      and t.refresh_hz > rate + 0.1])
                    _, dstart = profiles.PRIMARY.parse_cta_blocks(default)
                    others = {raw for _, raw, t in profiles.PRIMARY.parse_cta_dtds(default, dstart)
                              if (t.width, t.height) != (timing.width, height)}
                    self.assertTrue(others <= {raw for _, raw, _ in profiles.PRIMARY.parse_cta_dtds(data, start)})
                    vics = dict(profiles.PRIMARY.parse_cta_blocks(data)[0])[2]
                    self.assertFalse([v for v in vics if profiles.VIC_MODES[v & 0x7F][:2] == (timing.width, height)
                                      and profiles.VIC_MODES[v & 0x7F][2] > rate])
        for height in (720, 1080, 1440):
            self.assertEqual(profiles.build_rate_profile(self.source, height, profiles.RATES[height][0])[0],
                             profiles.build_profile(self.source, height)[0])

    def test_unreviewed_input_is_rejected(self):
        changed = bytearray(self.source)
        changed[10] ^= 1
        with self.assertRaises(ValueError):
            profiles.build_profile(bytes(changed), 1440)


if __name__ == "__main__":
    unittest.main()
