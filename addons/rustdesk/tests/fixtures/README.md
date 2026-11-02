These two wire fixtures are encoded by protoc using RustDesk 1.5.0's
`libs/base/protos/message.proto` at fada664df7a294d1d1a9ca3e7cd3637069122f17.
They contain dummy key text, not cryptographic credentials. They check that
our partial prost schema decodes the upstream field numbers and the absent
KX version (v0) as well as field 3 (v1). Encryption is tested separately.

The same fixtures decode and re-encode byte-for-byte with RustDesk 1.5.1's
schema at 23e5bb471a040d62f0a6ad52591fb30784265b73; PublicKey and its KX
version fields are unchanged. The fixture provenance above remains 1.5.0.
