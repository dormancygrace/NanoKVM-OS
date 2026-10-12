#!/usr/bin/env python3
"""Regress qualified portrait timing and safe fallback advertisement."""
import importlib.util
from pathlib import Path
import unittest
HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("portrait_edid", HERE / "build-portrait-edid.py")
g = importlib.util.module_from_spec(spec)
spec.loader.exec_module(g)

class PortraitEdidTests(unittest.TestCase):
    def test_standard_hd_profile(self):
        source = (HERE.parent / "tools/nanokvm_update_edid/E21_NanoKVM.bin").read_bytes()
        data = g.profile(source, g.HD_MODE)
        self.assertEqual([sum(data[i:i+128]) % 256 for i in (0, 128)], [0, 0])
        d = data[54:72]
        self.assertEqual(d[2] | ((d[4] >> 4) << 8), 720)
        self.assertEqual(d[5] | ((d[7] >> 4) << 8), 1280)
        self.assertEqual(int.from_bytes(d[:2], "little"), 13908)
        self.assertEqual(d[10] & 15, 10)
        self.assertAlmostEqual(139080000 / (880 * 1317), 120, places=2)

    def test_h265_maximum_profile(self):
        source = (HERE.parent / "tools/nanokvm_update_edid/E21_NanoKVM.bin").read_bytes()
        data = g.profile(source, g.MAX_MODE)
        self.assertEqual([sum(data[i:i+128]) % 256 for i in (0, 128)], [0, 0])
        self.assertEqual(data[54:56], (20840).to_bytes(2, "little"))
        self.assertAlmostEqual(208400000 / (1600 * 2605), 50, places=2)
        d = data[54:72]
        self.assertEqual(d[2] | ((d[4] >> 4) << 8), 1440)
        self.assertEqual(d[5] | ((d[7] >> 4) << 8), 2560)
        self.assertEqual(data[21:23], bytes((30, 53)))

    def test_h264_maximum_50hz_profile(self):
        source = (HERE.parent / "tools/nanokvm_update_edid/E21_NanoKVM.bin").read_bytes()
        data = g.profile(source, g.AVC_MODE)
        self.assertEqual([sum(data[i:i+128]) % 256 for i in (0, 128)], [0, 0])
        d = data[54:72]
        self.assertEqual(d[2] | ((d[4] >> 4) << 8), 1296)
        self.assertEqual(d[5] | ((d[7] >> 4) << 8), 2304)
        self.assertAlmostEqual(int.from_bytes(d[:2], "little") * 10000 / (1456 * 2349), 50, places=2)

    def test_qualified_timing_and_fallback(self):
        source = (HERE.parent / "tools/nanokvm_update_edid/E21_NanoKVM.bin").read_bytes()
        data = g.profile(source)
        self.assertEqual(len(data), 256)
        self.assertEqual([sum(data[i:i+128]) % 256 for i in (0, 128)], [0, 0])
        d = data[54:72]
        self.assertEqual(d[2] | ((d[4] >> 4) << 8), 1080)
        self.assertEqual(d[5] | ((d[7] >> 4) << 8), 1920)
        self.assertEqual(int.from_bytes(d[:2], "little"), 18368)
        self.assertEqual(d[10] & 15, 10)
        self.assertEqual(data[21:23], bytes((30, 53)))
        # Vendor and product as the source; the serial number is the identity
        # of the 1080x1920@75 rate profile.
        self.assertEqual(data[8:12], source[8:12])
        self.assertEqual(int.from_bytes(data[12:16], "little"), 1920 * 100 + 75)
        self.assertEqual(data[16:18], source[16:18])
        self.assertEqual(data[35:54], source[35:54])
        end = 128 + data[130]
        pos = 132
        while pos < end:
            self.assertNotEqual(data[pos] >> 5, 2, "No competing CTA video modes")
            pos += (data[pos] & 31) + 1
        fallback = data[end:end+18]
        self.assertEqual(fallback[2] | ((fallback[4] >> 4) << 8), 800)
        self.assertEqual(fallback[5] | ((fallback[7] >> 4) << 8), 600)
        self.assertFalse(any(data[end+18:255]))

class PortraitRateIdentityTests(unittest.TestCase):
    def test_rate_profiles_have_own_identity(self):
        """Windows reapplies the mode last used with a monitor identity; each
        portrait rate profile is a monitor of its own."""
        source = (HERE.parent / "tools/nanokvm_update_edid/E21_NanoKVM.bin").read_bytes()
        seen = set()
        for height, rates in g.RATES.items():
            width = g.WIDTHS[height]
            for rate in rates:
                data = g.profile(source, g.rate_mode(width, height, rate), rate)
                serial = int.from_bytes(data[12:16], "little")
                texts = [data[pos + 5:pos + 18].split(b"\n")[0].decode()
                         for pos in range(54, 126, 18) if data[pos:pos + 4] == b"\0\0\0\xff"]
                with self.subTest(height=height, rate=rate):
                    self.assertEqual(serial, height * 100 + rate)
                    self.assertEqual(texts, [f"NKVM{height}P{rate}"])
                    self.assertEqual(sum(data[:128]) % 256, 0)
                    self.assertNotIn(serial, seen)
                seen.add(serial)
            # The single-rate profile is the rate profile of its legacy rate.
            legacy_rate, legacy_mode = g.LEGACY[(width, height)]
            self.assertEqual(g.profile(source, legacy_mode),
                             g.profile(source, g.rate_mode(width, height, legacy_rate), legacy_rate))


if __name__ == "__main__":
    unittest.main()
