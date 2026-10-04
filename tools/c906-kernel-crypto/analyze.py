#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Analyze complete equal-work paired C906 kernel probe measurements."""
import argparse
import csv
import json
from pathlib import Path
import statistics

NAMES = ['ChaCha20', 'ChaCha12', 'AEAD_SG_encrypt', 'AEAD_SG_decrypt',
         'CRC32', 'CRC32C', 'copy_checksum']
FIELDS = ['operation', 'bytes', 'offset', 'dst_offset', 'pattern', 'seed', 'flags']

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('csv', type=Path)
    p.add_argument('json', type=Path)
    p.add_argument('--thresholds', action='store_true')
    a = p.parse_args()
    repetitions, calls, expected_rows = (7,32,4480) if a.thresholds else (5,8,2000)
    rows = list(csv.DictReader(a.csv.open()))
    groups = {}
    for row in rows:
        row = {k: int(v) for k, v in row.items()}
        if row['iterations'] != calls or row['elapsed_ns'] <= 0:
            raise ValueError('unexpected amount of work or elapsed time')
        key = tuple(row[k] for k in FIELDS)
        group = groups.setdefault(key, {})
        samples = group.setdefault(row['variant'], {})
        rep = row['repetition']
        if rep in samples:
            raise ValueError('duplicate repetition')
        samples[rep] = row['elapsed_ns'] / row['iterations']
    if len(rows) != expected_rows or len(groups) != 140:
        raise ValueError(f'incomplete default benchmark matrix: {len(rows)} rows, {len(groups)} groups')
    output = []
    for key, modes in sorted(groups.items()):
        op, n, offset, dst, pattern, seed, flags = key
        expected = ({0,4,5,6,7,8} if a.thresholds else {0,2,4}) if op <= 3 else {0,1,2} if op <= 5 else {0,2}
        if set(modes) != expected or any(set(v) != set(range(repetitions)) for v in modes.values()):
            raise ValueError('missing variant or repetition')
        baseline = modes[0]
        for mode, samples in sorted(modes.items()):
            if not mode:
                continue
            reductions = [100*(1-samples[rep]/baseline[rep]) for rep in range(repetitions)]
            output.append(dict(operation=op,name=NAMES[op],bytes=n,offset=offset,dst_offset=dst,
                pattern=pattern,seed=seed,flags=flags,variant=mode,
                baseline_median_ns=statistics.median(baseline.values()),
                candidate_median_ns=statistics.median(samples.values()),
                time_reduction_percent=100*(1-statistics.median(samples.values())/statistics.median(baseline.values())),
                paired_reduction_median_percent=statistics.median(reductions),
                paired_reduction_min_percent=min(reductions),paired_reduction_max_percent=max(reductions),
                faster_repetitions=sum(v>0 for v in reductions)))
    result = {'rows':len(rows),'groups':len(groups),'comparisons':output,
              'interpretation':'whole-function kernel elapsed time, context entry/exit included; no application/tunnel throughput claim'}
    a.json.write_text(json.dumps(result,indent=2)+'\n')
    for op in range(7):
        print(NAMES[op])
        for n in [64,256,512,1420,1500,4096,8192,16384,65536]:
            bits=[x for x in output if x['operation']==op and x['bytes']==n]
            for mode in sorted({x['variant'] for x in bits}):
                tests=[x for x in bits if x['variant']==mode]
                reductions=[x['time_reduction_percent'] for x in tests]
                print(f"  {n:5} mode{mode}: time reduction {min(reductions):7.2f}..{max(reductions):7.2f}% "
                      f"all{repetitions}_faster={sum(x['faster_repetitions']==repetitions for x in tests)}/{len(tests)}")

if __name__=='__main__':
    main()
