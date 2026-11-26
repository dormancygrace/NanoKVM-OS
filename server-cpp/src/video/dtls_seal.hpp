#pragma once

// DTLS 1.2 AES-128-GCM records sealed on the SG2002's CryptoDMA engine, the records of a data channel's burst in
// one request (HWCRYPTO_GCM_BATCH: AES-CTR on the engine, GHASH in the driver while it runs). Used by libdatachannel
// patch 0017 for connections that negotiated AES-128-GCM, which the RustDesk client always does. Without the engine,
// or once it failed, libdatachannel seals the records in software.

namespace nkos::video::dtls_seal {

// Installs the sealer for every connection. Once, at start.
void install();

} // namespace nkos::video::dtls_seal
