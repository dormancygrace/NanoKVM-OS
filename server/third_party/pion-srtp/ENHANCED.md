# NanoKVM Enhanced packet AES hook

Base: github.com/pion/srtp/v3 v3.0.14, upstream commit ba42f10 (release tag).
Upstream files and MIT license are retained. UPSTREAM-SHA256.json records the
pristine module contents; this is a local Go module replacement, not upstream
support for SG2002 hardware.

Changes: aes_ctr_accelerator.go adds a process-wide factory selection for future
AES-CM contexts; srtp_cipher_aes_cm_hmac_sha1.go wraps the two derived AES keys;
crypto.go invokes the optional backend once per CTR payload. Only AES-128 keys
are wrapped. Key derivation, SRTP/SRTCP counter generation, authentication,
Cryptex, replay handling, GCM and NULL remain upstream code. Failed/declined
requests use the original block CTR implementation.

The NanoKVM backend is server/internal/sg2002aes. It copies input into its own
ioctl buffer and copies output back only on success, including in-place calls.
Its shared descriptor/buffer is serialized and disabled permanently after the
first failure. The hook contract requires declined calls to preserve all inputs
and destination so software fallback is safe.

When updating Pion, compare this small delta with the new upstream, refresh the
base manifest, and retain the packet-level hook rather than an ioctl per block.
