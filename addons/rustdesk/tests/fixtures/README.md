These two wire fixtures are encoded by protoc using RustDesk 1.5.0's
`libs/base/protos/message.proto` at fada664df7a294d1d1a9ca3e7cd3637069122f17.
They contain dummy key text, not cryptographic credentials. They check that
our partial prost schema decodes the upstream field numbers and the absent
KX version (v0) as well as field 3 (v1). Encryption is tested separately.

The protocol schema in RustDesk 1.5.3 at
95cca3f661f635b3cc85c04935fe8d96d8f432ef is byte-for-byte identical to
1.5.1 (message.proto blob 5f920bc9a0f849eff226bc866dff6d93e69323f2).
The existing round-trip result therefore applies unchanged; the fixtures
were not regenerated for 1.5.3. Their provenance remains 1.5.0.
