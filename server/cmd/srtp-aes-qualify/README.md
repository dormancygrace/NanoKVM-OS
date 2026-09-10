# Physical SRTP AES diagnostic

This standalone program compares Pion AES128_CM_HMAC_SHA1_80 using the actual
NanoKVM software and SG2002 packet adapter. It requires the experimental device
module; it does not start a video or browser session.

For a short packet-size comparison:

    srtp-aes-qualify -payloads=512,768,1188 -packets=500 -rounds=3

Use UART/network observation and the existing recovery setup. Inputs are
validated before device open. Hardware exposure, including warmups and a
correctness reserve, is capped at 10000 operations. The 20-second outer budget
checks between operations and cannot cancel an ioctl stuck on a bus access.
Defaults retain one size (1188), 2000 packets and three alternating rounds.

The initial correctness set checks rollover, exact overlap, authentication,
SRTCP and fallback. Each selected size also receives a warmup-output comparison.
Timing checks errors and hardware hit counts but does not independently verify
every timed ciphertext. Output reports benchmark-process CPU, not whole-system
or server CPU. All session material is synthetic and must remain diagnostic.

See firmware/crypto/SRTP-PAYLOAD-COST-2026-09-09.md at the repository root for the
physical result and limitations; no production cutoff change is implied.
