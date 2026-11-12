These two wire fixtures are encoded by protoc using RustDesk 1.5.0's
`libs/base/protos/message.proto` at fada664df7a294d1d1a9ca3e7cd3637069122f17.
They contain dummy key text, not cryptographic credentials. They check that
our partial prost schema decodes the upstream field numbers and the absent
KX version (v0) as well as field 3 (v1). Encryption is tested separately.

The same fixtures decode and re-encode byte-for-byte with RustDesk 1.5.3's
schema at 95cca3f661f635b3cc85c04935fe8d96d8f432ef; PublicKey and its KX
version fields are unchanged. The fixture provenance above remains 1.5.0.
