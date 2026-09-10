/* SPDX-License-Identifier: GPL-2.0 */
#ifndef AICBSP_SDIO_IDS_H
#define AICBSP_SDIO_IDS_H
/* Primary IDs from this driver's chipmatch; secondary IDs from the vendor
 * constants. NanoKVM AIC8801 reports function2 as544a:0146 (live sysfs).
 * Keep exact pairs: accepting a vendor OR a device can claim another card. */
#define AICBSP_SDIO_IDS(X) \
	X(0x5449, 0x0145) \
	X(0x5449, 0x0146) \
	X(0x544a, 0x0146) \
	X(0xc8a1, 0xc08d) \
	X(0xc8a1, 0x0082) \
	X(0xc8a1, 0x0182) \
	X(0xc8a1, 0x9081) \
	X(0xc8a1, 0x9082) \
	X(0xc8a1, 0x9083) \
	X(0xc8a1, 0x9084) \
	X(0xc8a1, 0x9085) \
	X(0xc8a1, 0x9086) \
	X(0xc8a1, 0x2082)

static inline int aicbsp_sdio_id_supported(unsigned short vendor,
                                         unsigned short device)
{
#define AICBSP_ID_MATCH(v, d) if (vendor == (v) && device == (d)) return 1;
    AICBSP_SDIO_IDS(AICBSP_ID_MATCH)
#undef AICBSP_ID_MATCH
    return 0;
}
#endif
