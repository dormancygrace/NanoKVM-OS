module github.com/pion/sctp

require (
	github.com/pion/logging v0.2.4
	github.com/pion/randutil v0.1.0
	github.com/pion/transport/v5 v5.1.5
)

go 1.24.0

// Retract version with ZeroChecksum misinterpretation (bi-directional/global handling)
retract v1.8.12
