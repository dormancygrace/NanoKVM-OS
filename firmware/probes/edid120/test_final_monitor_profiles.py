#!/usr/bin/env python3
"""Validate the monitor EDID profiles.

Rule: a profile advertises no mode at or above its own resolution other than
its preferred timing; lower-resolution fallbacks stay.
"""
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
import build_final_monitor_profiles as profiles

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[2]
# CTA codes that occur in the accepted final EDID, written out independently of
# the module's table: code -> (width, height, Hz).
SOURCE_VICS = {1: (640, 480, 60), 4: (1280, 720, 60), 16: (1920, 1080, 60),
               19: (1280, 720, 50), 20: (1920, 1080, 50), 31: (1920, 1080, 50),
               47: (1280, 720, 120)}
STD_ASPECT = {0: (16, 10), 1: (4, 3), 2: (5, 4), 3: (16, 9)}


def dtd(raw):
    """(width, height, Hz) of a detailed timing descriptor."""
    clock = int.from_bytes(raw[:2], "little") * 10_000
    width, height = raw[2] | (raw[4] & 0xF0) << 4, raw[5] | (raw[7] & 0xF0) << 4
    total = (width + (raw[3] | (raw[4] & 0x0F) << 8)) * (height + (raw[6] | (raw[7] & 0x0F) << 8))
    return width, height, clock / total


def cta_layout(data):
    """(data blocks, DTDs) of the CTA extension by plain byte parsing."""
    end, pos, blocks = data[130], 132, []
    while pos < 128 + end:
        blocks.append((data[pos] >> 5, bytes(data[pos + 1:pos + 1 + (data[pos] & 31)])))
        pos += 1 + (data[pos] & 31)
    dtds = []
    pos = 128 + end
    while pos + 18 <= 255 and any(data[pos:pos + 18]):
        dtds.append(bytes(data[pos:pos + 18]))
        pos += 18
    return blocks, dtds


def advertised(data):
    """Every (kind, width, height, Hz or None) listed in the EDID, parsed
    without the module under test."""
    modes = []
    for pos in (54, 72, 90, 108):
        if data[pos] | data[pos + 1]:
            modes.append(("base-dtd",) + dtd(data[pos:pos + 18]))
    for pos in range(38, 54, 2):
        a, b = data[pos], data[pos + 1]
        if (a, b) != (1, 1):
            w = (a + 31) * 8
            modes.append(("standard", w, w * STD_ASPECT[b >> 6][1] // STD_ASPECT[b >> 6][0], (b & 63) + 60))
    blocks, dtds = cta_layout(data)
    for tag, payload in blocks:
        if tag == 2:
            modes += [("vic",) + SOURCE_VICS[v & 0x7F] + (v & 0x7F,) for v in payload]
    modes += [("cta-dtd",) + dtd(raw) for raw in dtds]
    return modes


class MonitorProfilesTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # The accepted final EDID is not stored: build it from the stock EDID
        # through the same chain as platform/build.sh.
        stock = REPO / "tools/nanokvm_update_edid/E21_NanoKVM.bin"
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            probes = REPO / "firmware/probes/edid120"
            steps = [
                (REPO / "scripts/build-qhd-edid.py", stock, "qhd30.bin", []),
                (probes / "build_experimental_edid.py", tmp / "qhd30.bin", "720p120.bin", ["--force"]),
                (probes / "build_experimental_qhd40.py", tmp / "720p120.bin", "qhd40.bin", ["--force"]),
                (probes / "build_experimental_fhd_high.py", tmp / "qhd40.bin", "final.bin",
                 ["--rate", "75", "--force"]),
            ]
            for script, source, target, extra in steps:
                subprocess.run([sys.executable, str(script), "--input", str(source),
                                "--output", str(tmp / target)] + extra,
                               check=True, stdout=subprocess.DEVNULL)
            cls.source = (tmp / "final.bin").read_bytes()
        _, start = profiles.PRIMARY.parse_cta_blocks(cls.source)
        cls.original = {bytes(raw) for _, raw, _ in profiles.PRIMARY.parse_cta_dtds(cls.source, start)}

    def every_profile(self):
        """(name, data, preferred timing) of every profile the generator emits."""
        for height in profiles.PREFERRED:
            data, timing = profiles.build_profile(self.source, height)
            yield f"monitor-{height}", data, timing
        for height, rates in profiles.RATES.items():
            for rate in rates:
                data, timing = profiles.build_rate_profile(self.source, height, rate)
                yield f"monitor-{height}-{rate}", data, timing

    def test_source_chain(self):
        self.assertEqual(profiles.sha256(self.source), profiles.EXPECTED_SOURCE_SHA256)

    def test_valid_edid(self):
        for name, data, timing in self.every_profile():
            with self.subTest(profile=name):
                self.assertEqual(len(data), 256)
                self.assertEqual(data[:8], bytes.fromhex("00ffffffffffff00"))
                self.assertEqual(data[126], 1)
                self.assertEqual([sum(data[i:i + 128]) % 256 for i in (0, 128)], [0, 0])
                # Identity, name/serial/range descriptors and preferred-mode
                # bit logic: only the preferred DTD (and the bit) change.
                self.assertEqual(data[:24], self.source[:24])
                self.assertEqual(data[24] & 3, 2)
                self.assertEqual(data[25:35], self.source[25:35])
                # (UHD30 raises the pixel clock limit of the range descriptor.)
                self.assertEqual(data[72:108], self.source[72:108])
                self.assertEqual(data[108:117], self.source[108:117])
                self.assertEqual(data[118:126], self.source[118:126])
                self.assertEqual(data[128:130], bytes([2, 3]))
                self.assertEqual(data[131], self.source[131])
                # The HDMI vendor block survives (UHD30 appends Max_TMDS_Clock).
                self.assertEqual([p for tag, p in cta_layout(data)[0] if tag == 3][0][:5],
                                 bytes.fromhex("030c001000"))

    def test_preferred_is_the_only_mode_at_or_above_its_resolution(self):
        for name, data, timing in self.every_profile():
            with self.subTest(profile=name):
                w, h = timing.width, timing.height
                above = [m for m in advertised(data) if m[1] >= w or m[2] >= h]
                self.assertTrue(above)
                # Base and CTA descriptors of the preferred timing only; a CTA
                # code is allowed when it is that same progressive timing.
                for kind, mw, mh, hz, *code in above:
                    self.assertIn(kind, ("base-dtd", "cta-dtd", "vic"))
                    self.assertEqual((mw, mh), (w, h))
                    self.assertAlmostEqual(hz, timing.refresh_hz, delta=0.5)
                self.assertEqual(profiles.modes_above_preferred(data), [])
                self.assertEqual(profiles.prune_to_preferred(data), data)

    def test_lower_resolution_fallbacks_remain(self):
        _, start = profiles.PRIMARY.parse_cta_blocks(self.source)
        for height in profiles.PREFERRED:
            full, preferred = profiles.full_profile(self.source, height)
            for name, data, timing in self.every_profile():
                if timing.height != height:
                    continue
                with self.subTest(profile=name):
                    kept = {(m["kind"], m["width"], m["height"], m.get("code"), m.get("hz"))
                            for m in profiles.list_modes(data)}
                    for m in profiles.list_modes(full):
                        if m["width"] < timing.width and m["height"] < timing.height:
                            if m["kind"] == "established":
                                continue  # checked below
                            key = (m["kind"], m["width"], m["height"], m.get("code"), m.get("hz"))
                            self.assertIn(key, kept)
                    # Established timings below the resolution stay.
                    for byte, bit, w, h in profiles.ESTABLISHED:
                        if w < timing.width and h < timing.height:
                            self.assertEqual(data[byte] & bit, self.source[byte] & bit)
        # Concrete expectations. 1080p keeps 720p, VGA, 1024x768, 1280x1024.
        data, _ = profiles.build_rate_profile(self.source, 1080, 75)
        self.assertEqual(data[35:38], self.source[35:38])
        modes = advertised(data)
        self.assertIn(("standard", 1280, 1024, 60), modes)
        self.assertEqual([m[4] for m in modes if m[0] == "vic"], [4, 19, 1, 47])
        self.assertEqual({(m[1], m[2], round(m[3])) for m in modes if m[0] == "cta-dtd"},
                         {(1280, 720, 60), (1280, 720, 120), (1920, 1080, 75)})
        # 720p keeps VGA and the 800x600 timings; QHD keeps all FHD/HD modes.
        data, _ = profiles.build_rate_profile(self.source, 720, 120)
        self.assertEqual([m[4] for m in advertised(data) if m[0] == "vic"], [1, 47])
        self.assertEqual(data[35] & 0x25, self.source[35] & 0x25)  # 640x480, 800x600@60
        data, _ = profiles.build_rate_profile(self.source, 1440, 60)
        modes = advertised(data)
        self.assertEqual([m[4] for m in modes if m[0] == "vic"], [4, 31, 20, 19, 1, 16, 47])
        self.assertEqual({(m[1], m[2], round(m[3])) for m in modes if m[0] == "cta-dtd"},
                         {(1920, 1080, 60), (1920, 1080, 75), (1280, 720, 60), (1280, 720, 120)})

    def test_fhd75_lists_no_other_fhd_or_qhd_timing(self):
        data, timing = profiles.build_rate_profile(self.source, 1080, 75)
        self.assertEqual((timing.width, timing.height), (1920, 1080))
        self.assertAlmostEqual(timing.refresh_hz, 75, delta=0.1)
        for kind, w, h, hz, *code in advertised(data):
            if h >= 1080 or w >= 1920:
                self.assertEqual((kind in ("base-dtd", "cta-dtd"), w, h, round(hz)),
                                 (True, 1920, 1080, 75))
        codes = {v & 0x7F for tag, payload in cta_layout(data)[0] if tag == 2 for v in payload}
        self.assertFalse(codes & {16, 20, 31})  # 1080p60, 1080i50, 1080p50
        self.assertFalse([t for _, _, t in profiles.PRIMARY.parse_cta_dtds(data, 128 + data[130])
                          if t.height >= 1080 and round(t.refresh_hz) != 75])

    def test_rate_profiles(self):
        for height, rates in profiles.RATES.items():
            for rate in rates:
                with self.subTest(height=height, rate=rate):
                    data, timing = profiles.build_rate_profile(self.source, height, rate)
                    self.assertEqual((timing.width, timing.height), (profiles.WIDTHS[height], height))
                    self.assertAlmostEqual(timing.refresh_hz, rate, delta=0.1)
                    rng = profiles.PRIMARY.find_range_descriptor(data)
                    self.assertGreaterEqual(data[rng + 9] * 10_000_000, timing.pixel_clock_hz)
        # NanoKVM-monitor-<height>.bin keeps its rate and bytes.
        for height, (_, _, rate) in profiles.PREFERRED.items():
            self.assertEqual(profiles.build_rate_profile(self.source, height, int(rate))[0],
                             profiles.build_profile(self.source, height)[0])

    def test_static_auto_profile_is_strict_1080p100(self):
        """Policy: the generated static Automatic profile stays single-rate.
        It prefers 1080p100 and deliberately has no 1080p60 fallback: with one
        it, the Windows GPU driver tested here would output the fallback
        instead of 100 Hz on every path that applies this file (install,
        Windows-pointer USB switch, missing rate profile). A source that
        cannot drive 100 Hz falls to 720p until a slower profile is chosen
        or the stream rate lowers the runtime-selected profile."""
        data, timing = profiles.build_auto_profile(self.source)
        self.assertEqual((timing.width, timing.height), (1920, 1080))
        self.assertAlmostEqual(timing.refresh_hz, 100, delta=0.1)
        self.assertEqual(data, profiles.build_rate_profile(self.source, 1080, 100)[0])
        modes = advertised(data)
        # No 1080p60 in any form: not as CTA VIC 16 (nor 20/31), not as a DTD.
        codes = {v & 0x7F for tag, payload in cta_layout(data)[0] if tag == 2 for v in payload}
        self.assertFalse(codes & {16, 20, 31, 32, 33, 34})
        self.assertFalse([m for m in modes if m[2] == 1080 and round(m[3]) == 60])
        # 1920x1080@100 is the only mode at or above 1080 lines or 1920 pixels.
        above = [m for m in modes if m[1] >= 1920 or m[2] >= 1080]
        self.assertTrue(above)
        for kind, w, h, hz, *code in above:
            self.assertEqual((kind in ("base-dtd", "cta-dtd"), w, h, round(hz)),
                             (True, 1920, 1080, 100))
        # The lower fallbacks stay: 720p60 and 720p120 (VIC 4, 19, 47 and DTDs).
        self.assertEqual([m[4] for m in modes if m[0] == "vic"], [4, 19, 1, 47])
        self.assertTrue({(1280, 720, 60), (1280, 720, 120)}
                        <= {(m[1], m[2], round(m[3])) for m in modes if m[0] == "cta-dtd"})

    def test_generated_auto_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            (tmp / "source.bin").write_bytes(self.source)
            subprocess.run([sys.executable, str(HERE / "build_final_monitor_profiles.py"),
                            "--input", str(tmp / "source.bin"), "--output", str(tmp / "out")],
                           check=True, stdout=subprocess.DEVNULL)
            auto = (tmp / "out/NanoKVM-monitor-auto.bin").read_bytes()
            self.assertEqual(auto, (tmp / "out/NanoKVM-monitor-1080-100.bin").read_bytes())
            self.assertEqual(auto, profiles.build_auto_profile(self.source)[0])

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
        # Every CTA mode of the unpruned QHD50 profile except the 720p60 DTD.
        qhd, _ = profiles.full_profile(self.source, 1440)
        _, qstart = profiles.PRIMARY.parse_cta_blocks(qhd)
        expected = {bytes(raw) for _, raw, t in profiles.PRIMARY.parse_cta_dtds(qhd, qstart)
                    if not (t.height == 720 and abs(t.refresh_hz - 60) < .05)}
        self.assertEqual(kept, expected)
        self.assertEqual(len(modes), len(expected))
        # UHD30 is the only mode at or above 3840x2160; the single-rate rule
        # removes nothing, so pruning leaves the profile byte for byte.
        self.assertEqual(profiles.modes_above_preferred(data), [])
        self.assertEqual(profiles.prune_to_preferred(data), data)

    def test_other_cta_blocks_survive(self):
        data, _ = profiles.build_rate_profile(self.source, 1080, 75)
        blocks, dtds = cta_layout(data)
        audio, speaker = (1, bytes.fromhex("097f07")), (4, bytes.fromhex("010000"))
        cta = b"".join(bytes([(tag << 5) | len(p)]) + p
                       for tag, p in [blocks[0], audio, blocks[1], speaker])
        ext = bytearray(128)
        ext[:4] = bytes([2, 3, 4 + len(cta), data[131]])
        ext[4:4 + len(cta)] = cta
        ext[4 + len(cta):4 + len(cta) + 18 * len(dtds)] = b"".join(dtds)
        ext[127] = (-sum(ext[:127])) & 0xFF
        extended = data[:128] + bytes(ext)
        pruned = profiles.prune_to_preferred(extended)
        self.assertEqual(pruned, extended)
        # With the full FHD/QHD mode list the same blocks are kept.
        full, _ = profiles.full_profile(self.source, 1080)
        self.assertEqual(profiles.prune_to_preferred(full)[128:], data[128:])

    def test_vic_table(self):
        for code, size in SOURCE_VICS.items():
            self.assertEqual(profiles.vic_entry(code)["width"], size[0])
            self.assertEqual(profiles.vic_entry(code)["height"], size[1])
            self.assertEqual(profiles.vic_entry(code)["hz"], size[2])
        self.assertEqual(len(profiles.CTA_VICS), 154)  # 1-127 and 193-219
        self.assertEqual(profiles.vic_entry(31)["hz"], 50)
        self.assertTrue(profiles.vic_entry(20)["interlaced"])  # 1080i50
        self.assertEqual(profiles.vic_entry(0x90)["height"], 1080)  # native 1080p60
        self.assertEqual((profiles.vic_entry(95)["height"], profiles.vic_entry(95)["hz"]), (2160, 30))
        self.assertEqual(profiles.vic_entry(0xC1)["width"], 5120)  # 193, not native
        for code in (0, 128, 220, 255):
            with self.assertRaises(ValueError):
                profiles.vic_entry(code)

    def test_unreviewed_input_is_rejected(self):
        changed = bytearray(self.source)
        changed[10] ^= 1
        with self.assertRaises(ValueError):
            profiles.build_profile(bytes(changed), 1440)


if __name__ == "__main__":
    unittest.main()
