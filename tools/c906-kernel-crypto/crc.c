// SPDX-License-Identifier: GPL-2.0-only
#include <linux/types.h>
#include <linux/minmax.h>
#include <linux/unaligned.h>
#include "candidates.h"
struct kc_crc_tables { u32 slice[8][256], advance[4][256]; };
static struct kc_crc_tables tables[2];
extern void kc_crc_lanes(const u8 *, const u32 *, u32 *, size_t);
static u32 bytes_crc(u32 crc, const u8 *p, size_t n, const u32 *t)
{
	while (n--) crc = t[(crc ^ *p++) & 255] ^ (crc >> 8);
	return crc;
}
void kc_tables_init(void)
{
	unsigned int kind, i, j, k;
	for (kind = 0; kind < 2; kind++) {
		u32 poly = kind ? 0x82f63b78 : 0xedb88320;
		struct kc_crc_tables *t = &tables[kind];
		for (i = 0; i < 256; i++) {
			u32 c = i;
			for (j = 0; j < 8; j++) c = (c >> 1) ^ ((0U - (c & 1)) & poly);
			t->slice[0][i] = c;
		}
		for (k = 1; k < 8; k++) for (i = 0; i < 256; i++) {
			u32 c = t->slice[k-1][i];
			t->slice[k][i] = t->slice[0][c & 255] ^ (c >> 8);
		}
		for (k = 0; k < 4; k++) for (i = 0; i < 256; i++) {
			u32 c = i << (8*k);
			for (j = 0; j < 256; j++) c = t->slice[0][c & 255] ^ (c >> 8);
			t->advance[k][i] = c;
		}
	}
}
u32 kc_crc_scalar(u32 crc, const u8 *p, size_t n, bool kind)
{
	const u32 (*t)[256] = tables[kind].slice;
	while (n >= 8) {
		crc ^= get_unaligned_le32(p);
		crc = t[7][crc & 255] ^ t[6][(crc >> 8) & 255] ^
			t[5][(crc >> 16) & 255] ^ t[4][crc >> 24] ^
			t[3][p[4]] ^ t[2][p[5]] ^ t[1][p[6]] ^ t[0][p[7]];
		p += 8; n -= 8;
	}
	return bytes_crc(crc, p, n, t[0]);
}
u32 kc_crc_vector(u32 crc, const u8 *p, size_t n, bool kind)
{
	const struct kc_crc_tables *t = &tables[kind];
	u32 lanes[16] __aligned(16);
	while (n >= 512) {
		size_t count = min_t(size_t, n / 256, 16), i;
		kc_crc_lanes(p, t->slice[0], lanes, count);
		for (i = 0; i < count; i++)
			crc = t->advance[0][crc & 255] ^ t->advance[1][(crc >> 8) & 255] ^
				t->advance[2][(crc >> 16) & 255] ^ t->advance[3][crc >> 24] ^ lanes[i];
		p += count * 256; n -= count * 256;
	}
	return kc_crc_scalar(crc, p, n, kind);
}
