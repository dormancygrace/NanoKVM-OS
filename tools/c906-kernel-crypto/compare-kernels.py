#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Compare equal-work direct production-library kernel benchmark matrices."""
import argparse
import csv
import json
from pathlib import Path
import statistics

NAMES = ['ChaCha20','ChaCha12','AEAD_SG_encrypt','AEAD_SG_decrypt','CRC32','CRC32C','copy_checksum']
FIELDS = ['operation','bytes','offset','dst_offset','pattern','seed','flags']

def read(path):
    groups = {}
    rows = list(csv.DictReader(path.open()))
    if len(rows) != 980:
        raise ValueError(f'incomplete kernel matrix: {path}: {len(rows)} rows')
    for row in rows:
        row = {k:int(v) for k,v in row.items()}
        if row['variant'] or row['iterations'] != 32 or row['elapsed_ns'] <= 0:
            raise ValueError('unexpected mode/work/timing')
        key = tuple(row[k] for k in FIELDS)
        samples = groups.setdefault(key,{})
        rep = row['repetition']
        if rep in samples:
            raise ValueError('duplicate repetition')
        samples[rep] = row['elapsed_ns']/32
    if len(groups) != 140 or any(set(x) != set(range(7)) for x in groups.values()):
        raise ValueError('missing group/repetition')
    return groups

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('baseline',type=Path)
    p.add_argument('candidate',type=Path)
    p.add_argument('json',type=Path)
    a = p.parse_args()
    old,new = read(a.baseline),read(a.candidate)
    if old.keys() != new.keys():
        raise ValueError('unequal inputs')
    comparisons = []
    for key in sorted(old):
        op,n,src,dst,pattern,seed,flags = key
        before,after = statistics.median(old[key].values()),statistics.median(new[key].values())
        comparisons.append(dict(operation=op,name=NAMES[op],bytes=n,offset=src,dst_offset=dst,
            pattern=pattern,seed=seed,flags=flags,baseline_median_ns=before,candidate_median_ns=after,
            time_reduction_percent=100*(1-after/before),
            faster_repetitions=sum(new[key][rep]<old[key][rep] for rep in range(7))))
    a.json.write_text(json.dumps({'comparisons':comparisons,'groups':140,
        'interpretation':'direct kernel library elapsed time including context entry/exit; application throughput is not measured'},indent=2)+'\n')
    for op in range(7):
        print(NAMES[op])
        for n in [1420,1500,4096,8192,16384,65536]:
            rows = [x for x in comparisons if x['operation']==op and x['bytes']==n]
            print(n,[(round(x['time_reduction_percent'],2),x['faster_repetitions']) for x in rows])

if __name__ == '__main__':
    main()
