# NanoKVM DTLS changes

Base: `github.com/pion/dtls/v3 v3.1.15`. The upstream MIT license is retained;
only the NanoKVM tests (`nanokvm_*_test.go`) are kept. The original module is available through the Go module proxy for comparison.

NanoKVM changes the DTLS 1.2 server's cipher-suite intersection order in
`negotiateClientHello` in `flight0handler.go` (including the cookie retry): the server walks its configured suites first and chooses
the first suite also offered by the client, including the v3.1.14 SNI
certificate-filtered selection. Client-side selection, certificate
filtering, signature verification, cryptographic implementations, and SRTP
profile negotiation remain upstream behavior. Since v3.1.12 a server whose
configured curves share none with the client falls back to a non-ECDHE suite;
that fallback walks the server's order as well. NanoKVM configures only ECDHE
suites, so production connections never reach it.

The transport interface is v5.

`Conn.Read` drains an already authenticated, buffered application record before
returning EOF after closure. The previous select could randomly choose the
closed signal over the final record; the unmodified upstream BasicIO test
reproduced this 9 times in 100 runs during integration. Cryptographic validation is unchanged.

`Conn.Write` of application data is shorter (a data channel sends about 830
records for a megabyte):

- Without a write deadline nothing is made or registered for the record
  (`writeDirect` in `conn.go`). Upstream makes a context and starts two
  goroutines for every record, and `WriteToContext` of pion/transport registers
  a cancellation and removes it again. What they give is kept: when the
  connection closes, or a deadline set meanwhile passes, while the write is in
  the socket, the write deadline of the next connection moves to the past and
  the writer puts it back. The close is watched with one registration for the
  connection, a deadline with one for each deadline set during such a write.
  With a write deadline already set the upstream code runs.
- With an AES-GCM or ChaCha20-Poly1305 suite and no connection ID the record is
  built and sealed in one buffer the connection keeps (`writeApplicationData`
  in `conn.go`, `SealApplicationData` of the suites) instead of three
  allocated copies of the payload. The bytes on the wire are the same:
  `nanokvm_seal_test.go` compares them with `Encrypt` of the marshaled record.
  Other suites and records with a connection ID take the upstream path. The
  buffer is reused by the next write, which relies on `net.PacketConn` not
  keeping what `WriteTo` was given.

The read side of a data channel is one small record for every two packets
sent (SCTP's acknowledgements), so it is as frequent as the write side:

- The reader takes datagrams from the next connection itself (`readDirect`).
  `ReadFromContext` registers a cancellation and removes it again for every
  datagram; the reader's context is the same for all of them and is watched
  with one registration, which moves the next connection's read deadline to the
  past when the context is cancelled.
- A record is copied for the handshake fragment buffer only when it is a
  handshake record; the buffer only looks at any other.

`Conn.WriteCopies` is an empty method that says what is true of `Write` here
and upstream: the payload has been copied or sent when it returns. The SCTP
fork reuses its packet buffers only for a connection that says so.

Checked with the upstream tests of v3.1.15 put back beside the fork (they are
not kept here): the same two fail as before the change (`TestHandshaker`,
`TestHandshakeSignatureSchemeBinding`); `TestNetTest`, the net.Conn conformance
test with its deadline and close cases, passes.

The v3.1.13 integration retains upstream's handshake authentication and resource
limits, including Finished verification, signature/key binding, cookie-retry
negotiation, RSA key-size limits and bounded transcript caching.
NanoKVM tests cover malformed CBC records and empty/padded connection-ID
inner plaintext. No production cryptographic implementation is changed locally.

The v3.1.14 integration preserves upstream certificate/key-type, ALPN, cookie,
record-epoch and session authentication checks. Legacy or incomplete saved
sessions require a fresh handshake. Local boundary tests cover large PSK hints,
malformed certificate entries, session role/EMS/cipher/key-policy changes,
incomplete serialized state, and unauthenticated post-handshake records.
The NanoKVM cipher preference test also detects a missed port at
the new SNI certificate-filtered intersection. All original NanoKVM patches
listed above remain; Pion ICE is unchanged.

The v3.1.15 integration adds upstream ECDHE-PSK ChaCha20-Poly1305 support
and preserves exponential backoff for repeated cookie responses. The new
suite is opt-in and does not alter NanoKVM's configured certificate suites.
Upstream's expanded state-restoration warnings are retained; NanoKVM uses
fresh WebRTC handshakes, not exported DTLS connection snapshots. The local
server cipher preference and buffered-record EOF handling are unchanged.
