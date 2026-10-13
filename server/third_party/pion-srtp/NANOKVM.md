# NanoKVM Enhanced packet AES hook

Base: github.com/pion/srtp/v3 v3.1.3 (release tag).
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

Frame batching (batch.go, plus small hooks in session_srtp.go and
srtp_cipher_aes_cm_hmac_sha1.go): when a HMACSHA1Batcher factory is set,
SessionSRTP gathers RTP packets of one SSRC that arrive in sequence and
protects them together at the marker bit (the last packet of a video frame),
at 32 packets, or 2 ms after the last one. Packets of other SSRCs and
out-of-order packets are written at once; the latter flush the pending run
first. Counters and rollover state are advanced per packet in order, exactly
as encryptRTP does; payloads are encrypted per packet (through the optional
AES-CTR accelerator) and the frame is authenticated in one batch call.
Cryptex, MKI, RCC modes and other profiles take the per-packet path.
Close marks the batch closed before it closes the transport and only then drops
the queue, so a flush blocked in a transport write cannot hold up shutdown;
after Close nothing is enqueued, flushed or written.
batch_test.go checks byte equality with EncryptRTP across the sequence-number
wrap and the session behaviour.

When updating Pion, compare this small delta with the new upstream, refresh the
base manifest, and retain the packet-level hook rather than an ioctl per block.
