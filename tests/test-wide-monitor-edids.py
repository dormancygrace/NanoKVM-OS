#!/usr/bin/env python3
"""The 16:10 and 21:9 monitor profiles: timing and identity."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


g = load("wide_edids", ROOT / "scripts/build-wide-monitor-edids.py")
# The validator of the 16:9 profiles: every advertised mode, not only the DTDs.
final = load("final_monitor_profiles", ROOT / "firmware/probes/edid120/build_final_monitor_profiles.py")
rgb = load("edid_rgb_range", ROOT / "scripts/edid-rgb-range.py")
SOURCE = (ROOT / "support/nanokvm_update_edid/E21_NanoKVM.bin").read_bytes()


def dtd(data):
    d = data[54:72]
    clock = int.from_bytes(d[:2], "little") * 10_000
    width, hblank = d[2] | (d[4] >> 4) << 8, d[3] | (d[4] & 15) << 8
    height, vblank = d[5] | (d[7] >> 4) << 8, d[6] | (d[7] & 15) << 8
    return width, height, clock / ((width + hblank) * (height + vblank)), clock


class WideMonitorEdidTests(unittest.TestCase):
    def test_every_profile_and_rate(self):
        serials = set()
        for (width, height), (vsync, rates) in g.PROFILES.items():
            for rate in rates:
                with self.subTest(size=(width, height), rate=rate):
                    data = g.profile(SOURCE, width, height, rate, vsync)
                    self.assertEqual([sum(data[i:i + 128]) % 256 for i in (0, 128)], [0, 0])
                    w, h, hz, clock = dtd(data)
                    self.assertEqual((w, h), (width, height))
                    self.assertAlmostEqual(hz, rate, delta=rate * 0.002)
                    self.assertLessEqual(clock, g.MAX_CLOCK_MHZ * 1_000_000)
                    self.assertEqual(data[24] & 3, 2)  # preferred DTD, no GTF inference
                    serial = int.from_bytes(data[12:16], "little")
                    self.assertNotIn(serial, serials)
                    self.assertGreater(serial, 2160 * 100 + 120)  # above every 16:9 serial
                    serials.add(serial)
                    # The build adds the RGB range declaration to every profile.
                    with_range = rgb.with_rgb_range(data)
                    self.assertEqual([sum(with_range[i:i + 128]) % 256 for i in (0, 128)], [0, 0])

    def test_no_mode_at_or_above_the_preferred_timing(self):
        for (width, height), (vsync, rates) in g.PROFILES.items():
            for rate in rates:
                with self.subTest(size=(width, height), rate=rate):
                    data = g.profile(SOURCE, width, height, rate, vsync)
                    self.assertEqual(final.modes_above_preferred(data), [])
                    modes = {(m["width"], m["height"]) for m in final.list_modes(data)}
                    self.assertIn((800, 600), modes)  # the legacy fallback stays

    def test_screen_size_follows_the_aspect_ratio(self):
        data = g.profile(SOURCE, 3440, 1440, 40, 10)
        self.assertEqual(data[21:23], bytes((60, 25)))

    def test_writes_the_server_file_names(self):
        with tempfile.TemporaryDirectory() as out:
            source = Path(out) / "E21.bin"
            source.write_bytes(SOURCE)
            import subprocess, sys
            subprocess.run([sys.executable, str(ROOT / "scripts/build-wide-monitor-edids.py"),
                            "--input", str(source), "--output", out], check=True, capture_output=True)
            names = sorted(p.name for p in Path(out).glob("NanoKVM-monitor-*.bin"))
            self.assertEqual(names, sorted(f"NanoKVM-monitor-{w}x{h}-{r}.bin"
                                           for (w, h), (_, rs) in g.PROFILES.items() for r in rs))


if __name__ == "__main__":
    unittest.main()
