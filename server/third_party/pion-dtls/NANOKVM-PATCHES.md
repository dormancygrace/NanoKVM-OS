# NanoKVM DTLS changes

Base: `github.com/pion/dtls/v3 v3.1.8`. The upstream MIT license and tests are
retained. The original module checksum remains in the parent `go.sum`.

NanoKVM changes only the DTLS 1.2 server's cipher-suite intersection order in
`flight0handler.go`: the server walks its configured suites first and chooses
the first suite also offered by the client. Client-side selection, certificate
filtering, signature verification, cryptographic implementations, and SRTP
profile negotiation remain upstream behavior.
