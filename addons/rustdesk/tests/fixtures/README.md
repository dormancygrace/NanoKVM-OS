These two wire fixtures are encoded by protoc using RustDesk 1.5.0's
`libs/base/protos/message.proto` at fada664df7a294d1d1a9ca3e7cd3637069122f17.
They contain dummy key text, not cryptographic credentials. They check that
our partial prost schema decodes the upstream field numbers and the absent
KX version (v0) as well as field 3 (v1). Encryption is tested separately.
