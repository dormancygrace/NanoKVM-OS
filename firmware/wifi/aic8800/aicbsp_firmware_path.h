/* SPDX-License-Identifier: GPL-2.0 */
#ifndef AICBSP_FIRMWARE_PATH_H
#define AICBSP_FIRMWARE_PATH_H

#define AICBSP_FIRMWARE_ROOT "aic8800_sdio"

static inline const char *aicbsp_firmware_subdir(unsigned int chipid)
{
	switch (chipid) {
	case PRODUCT_ID_AIC8801:
	case PRODUCT_ID_AIC8800D80:
		return "aic8800_and_aic8800D80";
	case PRODUCT_ID_AIC8800DC:
	case PRODUCT_ID_AIC8800DW:
		return "aic8800DC";
	case PRODUCT_ID_AIC8800D80N:
	case PRODUCT_ID_AIC8800D80WN:
		return "aic8800D80N";
	case PRODUCT_ID_AIC8800D80X2:
		return "aic8800D80X2";
	default:
		return NULL;
	}
}

static inline int aicbsp_firmware_request_name(unsigned int chipid,
						const char *name,
						char *path, size_t path_size)
{
	const char *subdir = aicbsp_firmware_subdir(chipid);
	int length;

	if (!subdir || !name || !*name || !path || !path_size || strchr(name, '/'))
		return -EINVAL;

	length = snprintf(path, path_size, "%s/%s/%s",
			  AICBSP_FIRMWARE_ROOT, subdir, name);
	if (length < 0 || (size_t)length >= path_size)
		return -ENAMETOOLONG;

	return 0;
}

#endif
