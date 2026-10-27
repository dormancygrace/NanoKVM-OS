# SPDX-License-Identifier: GPL-2.0-only
import random

def corpus(path):
    rng = random.Random(0x9062026)
    pages = []
    for entropy in [0, 40, 400, 1024, 2048, 3072, 3584, 3840, 4000, 4096]:
        for _ in range(16):
            block = bytearray(4096)
            block[:entropy] = rng.randbytes(entropy)
            rng.shuffle(block)
            pages.append(bytes(block))
    for period in [1, 2, 3, 4, 7, 8, 16, 31, 128, 1024]:
        for _ in range(8):
            pattern = rng.randbytes(period)
            pages.append((pattern * (4096 // period + 1))[:4096])
    for i in range(32):
        record = ('{"id":%d,"enabled":true,"name":"kernel page contract",'
                  '"items":[17,42,17,42]}\n' % i).encode()
        pages.append((record * (4096 // len(record) + 1))[:4096])
    for i in range(32):
        pages.append(b''.join((0x10000000 + ((j + i) % 97) * 16).to_bytes(8, 'little')
                              for j in range(512)))
    path.write_bytes(b''.join(pages))
    return len(pages)
