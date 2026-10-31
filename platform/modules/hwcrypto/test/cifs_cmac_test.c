// SPDX-License-Identifier: GPL-2.0-only
/* Exercise the actual CIFS size/provider helper from the patched kernel tree. */
#include <crypto/aes-cbc-macs.h>
#include <linux/module.h>
#include <linux/scatterlist.h>
#include <linux/slab.h>
#include "smb3_cmac.h"

static unsigned int size;
module_param(size, uint, 0444);
static bool hardware = true;
module_param(hardware, bool, 0444);

static int lengths(void)
{
	struct kvec iov[2] = {{ .iov_len = 64 }, { .iov_len = 16 }};
	size_t head, total;
	unsigned int n;

	/* Header vectors and payload must all participate in threshold selection. */
	for (n = 318; n <= 322; n++)
		if (smb3_cmac_length(iov, 2, n - 80, &head, &total) ||
		    head != 80 || total != n)
			return -EINVAL;
	if (smb3_cmac_length(iov, 0, 320, &head, &total) != -EINVAL)
		return -EINVAL;
	iov[0].iov_len = 4;
	if (smb3_cmac_length(iov, 1, 320, &head, &total) != -EINVAL)
		return -EINVAL;
	iov[0].iov_len = SIZE_MAX;
	if (smb3_cmac_length(iov, 2, 0, &head, &total) != -EOVERFLOW ||
	    smb3_cmac_length(iov, 1, 1, &head, &total) != -EOVERFLOW)
		return -EINVAL;
	iov[0].iov_len = UINT_MAX;
	if (smb3_cmac_length(iov, 1, 1, &head, &total) != -EOVERFLOW ||
	    smb3_cmac_length(iov, 1, 0, &head, &total) || total != UINT_MAX)
		return -EINVAL;
	return 0;
}

static int check(unsigned int len)
{
	struct crypto_ahash *tfm = smb3_cmac_alloc(len);
	struct ahash_request *req = NULL;
	struct aes_cmac_key ref __cleanup(aes_cmac_zeroize_key);
	struct scatterlist sg[3];
	DECLARE_CRYPTO_WAIT(wait);
	u8 key[16] = {1}, tag[16], expected[16], *buf = kmalloc(len, GFP_KERNEL);
	const char *expected_driver = hardware && len >= 320 ?
		"nanokvm-cmac-aes" : "cmac-aes-lib";
	int err = -ENOMEM;

	if (IS_ERR(tfm)) { err = PTR_ERR(tfm); goto out; }
	if (!buf)
		goto out;
	if (strcmp(crypto_ahash_driver_name(tfm), expected_driver)) {
		pr_err("cifs_cmac_test: len=%u driver=%s expected=%s\n", len,
			crypto_ahash_driver_name(tfm), expected_driver);
		err = -EINVAL;
		goto out;
	}
	memset(buf, 0x5a, len);
	err = aes_cmac_preparekey(&ref, key, 16) ?: crypto_ahash_setkey(tfm, key, 16);
	if (err)
		goto out;
	aes_cmac(&ref, buf, len, expected);
	req = ahash_request_alloc(tfm, GFP_KERNEL);
	if (!req) { err = -ENOMEM; goto out; }
	ahash_request_set_callback(req, CRYPTO_TFM_REQ_MAY_SLEEP |
				  CRYPTO_TFM_REQ_MAY_BACKLOG, crypto_req_done, &wait);
	/* Two header fragments plus data, with a block split at the first byte. */
	sg_init_table(sg, 3);
	sg_set_buf(sg, buf, 1);
	sg_set_buf(sg + 1, buf + 1, 63);
	sg_set_buf(sg + 2, buf + 64, len - 64);
	ahash_request_set_crypt(req, sg, tag, len);
	err = crypto_wait_req(crypto_ahash_digest(req), &wait);
	if (!err && memcmp(tag, expected, 16))
		err = -EBADMSG;
	if (!err)
		pr_info("cifs_cmac_test: PASS len=%u driver=%s\n", len, expected_driver);
out:
	ahash_request_free(req);
	if (!IS_ERR(tfm))
		crypto_free_ahash(tfm);
	kfree_sensitive(buf);
	return err;
}

static int __init cifs_cmac_test_init(void)
{
	static const unsigned int sizes[] = {128,288,303,304,318,319,320,321,322,384,512,4096};
	unsigned int i;
	int err = lengths();

	if (err)
		return err;
	if (size)
		return size < 80 || size > 65536 ? -EINVAL : check(size);
	for (i = 0; i < ARRAY_SIZE(sizes); i++) {
		err = check(sizes[i]);
		if (err)
			return err;
	}
	pr_info("cifs_cmac_test: PASS length/overflow and provider boundary checks\n");
	return 0;
}
static void __exit cifs_cmac_test_exit(void) {}
module_init(cifs_cmac_test_init);
module_exit(cifs_cmac_test_exit);
MODULE_DESCRIPTION("CIFS CMAC CPU/DMA selection qualification");
MODULE_LICENSE("GPL");
