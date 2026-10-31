# Scalar Go GHASH experiment

Adapted from Go 1.27.1 under its included BSD license. Build this standalone package with `GOOS=linux GOARCH=riscv64 CGO_ENABLED=0 go build -trimpath -o ghash-go-bench .`, then run on the device. It checks 10,000 multiplications against a bit-serial reference, compares original/unrolled full GHASH, and measures both plus standard AES-GCM. Synthetic fixed nonces are for benchmarking only.

`runtime-unroll.patch` replaces only the standard library GHASH multiply helper. `runtime-source.sha256` pins the original file. Apply to an isolated Go source copy or use `go build -overlay`; do not overwrite a shared installed GOROOT. The recorded overlay benchmark used Go 1.27.1 and changed no installed server or selected firmware runtime. Full release qualification of a modified Go cryptography implementation remains separate.
