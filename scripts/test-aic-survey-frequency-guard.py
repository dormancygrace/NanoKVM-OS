#!/usr/bin/env python3
"""Exercise the patched SDIO survey handler against a vendor source file."""
import argparse
from pathlib import Path
import subprocess
import tempfile

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source-file', type=Path, required=True)
a = p.parse_args()
script = Path(__file__).with_name('apply-aic-survey-frequency-guard.py')

with tempfile.TemporaryDirectory(prefix='aic-survey-guard-') as directory:
    root = Path(directory)
    dest = root / 'aic8800_fdrv/rwnx_msg_rx.c'
    dest.parent.mkdir()
    original = a.source_file.read_bytes().replace(b'\r\n', b'\n')
    assert b'BUG_ON(1);' in original, 'Expected unpatched vendor source'
    dest.write_bytes(original)
    cmd = ['python3', str(script), '--source', str(root)]
    subprocess.run(cmd, check=True)
    patched = dest.read_bytes()
    assert patched != original and b'BUG_ON(1);' not in patched
    assert b'idx < 0 || idx >= (int)ARRAY_SIZE(rwnx_hw->survey)' in patched
    subprocess.run(cmd, check=True)
    assert dest.read_bytes() == patched, 'Repeat application changed the source'

    text = patched.decode('utf-8')
    def extract(start, end):
        begin = text.index(start)
        return text[begin:text.index(end, begin)]

    lookup = extract('static int rwnx_freq_to_idx(', '/***************************************************************************')
    survey = extract('static inline int rwnx_rx_channel_survey_ind(',
                     'static inline int rwnx_rx_p2p_noa_upd_ind(')
    harness = '''#include <assert.h>
#include <errno.h>
#include <stddef.h>
#define CONFIG_RWNX_FULLMAC 1
#define NL80211_BAND_2GHZ 0
#define NUM_NL80211_BANDS 2
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
#define SURVEY_INFO_TIME 1
#define SURVEY_INFO_TIME_BUSY 2
#define SURVEY_INFO_NOISE_DBM 4
#define pr_warn_ratelimited(...) ((void)0)
struct ieee80211_channel { int center_freq; };
struct ieee80211_supported_band {
    int n_channels;
    struct ieee80211_channel *channels;
};
struct wiphy { struct ieee80211_supported_band *bands[NUM_NL80211_BANDS]; };
struct rwnx_survey_info {
    int chan_time_ms, chan_time_busy_ms, noise_dbm;
    unsigned int filled;
};
struct rwnx_hw {
    struct wiphy *wiphy;
    struct rwnx_survey_info survey[3];
    unsigned int sentinel;
};
struct rwnx_cmd;
struct mm_channel_survey_ind {
    int freq, chan_time_ms, chan_time_busy_ms, noise_dbm;
};
struct ipc_e2a_msg { void *param; };
''' + lookup + survey + '''
int main(void)
{
    struct ieee80211_channel channels24[] = {{2412}, {2437}};
    struct ieee80211_channel channels5[] = {{5180}, {5200}};
    struct ieee80211_supported_band band24 = {2, channels24};
    struct ieee80211_supported_band band5 = {2, channels5};
    struct wiphy phy = {{&band24, &band5}};
    struct rwnx_hw hw = {.wiphy = &phy, .sentinel = 0x12345678};
    struct mm_channel_survey_ind ind = {5180, 10, 3, -55};
    struct ipc_e2a_msg msg = {&ind};

    assert(rwnx_freq_to_idx(&hw, 2412) == 0);
    assert(rwnx_freq_to_idx(&hw, 2437) == 1);
    assert(rwnx_freq_to_idx(&hw, 5180) == 2);
    assert(rwnx_freq_to_idx(&hw, 5200) == 3);
    assert(rwnx_freq_to_idx(&hw, 9999) == -ENOENT);
    assert(rwnx_rx_channel_survey_ind(&hw, 0, &msg) == 0);
    assert(hw.survey[2].chan_time_ms == 10);
    assert(hw.survey[2].filled == (SURVEY_INFO_TIME | SURVEY_INFO_TIME_BUSY |
                                    SURVEY_INFO_NOISE_DBM));

    ind.freq = 5200; /* Valid frequency, but index equals survey length. */
    assert(rwnx_rx_channel_survey_ind(&hw, 0, &msg) == 0);
    assert(hw.sentinel == 0x12345678);
    ind.freq = 9999;
    assert(rwnx_rx_channel_survey_ind(&hw, 0, &msg) == 0);
    assert(hw.survey[0].filled == 0);
    assert(hw.sentinel == 0x12345678);

    phy.bands[0] = 0; /* Missing 2.4 GHz band does not invent a channel. */
    assert(rwnx_freq_to_idx(&hw, 5180) == 0);
    assert(rwnx_freq_to_idx(&hw, 2412) == -ENOENT);
    return 0;
}
'''
    test_source = root / 'survey-contract.c'
    executable = root / 'survey-contract'
    test_source.write_text(harness)
    subprocess.run(['cc', '-std=gnu11', '-Wall', '-Wextra', '-Werror',
                    '-Wno-unused-parameter', '-fsanitize=address,undefined',
                    '-fno-omit-frame-pointer', str(test_source), '-o', str(executable)],
                   check=True)
    subprocess.run([str(executable)], check=True)

    dest.write_bytes(b'/* unrelated source */\n')
    before = dest.read_bytes()
    assert subprocess.run(cmd, stdout=subprocess.DEVNULL,
                          stderr=subprocess.DEVNULL).returncode != 0
    assert dest.read_bytes() == before, 'Unknown source was modified'

print('PASS: guarded survey lookup, boundary, repeat application and source rejection')
