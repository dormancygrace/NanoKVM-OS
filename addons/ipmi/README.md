# IPMI add-on

`nanokvm-ipmi` is an independently versioned, optional IPMI-over-LAN daemon.
See [BMC settings, supported commands and security](../../docs/BMC.md).

- `cmd/nkos-ipmi/`: daemon entry point.
- `internal/ipmi/`: RMCP+ protocol implementation and tests.
- `addon.json`: package version, minimum OS version and installed files.
- `nanokvm-ipmi.*`: OpenRC service and package lifecycle scripts.

This directory is a separate Go module. Its local replacement of
`NanoKVM-Server` points to `../../server` so it shares `server/pkg/bmc` with
Redfish, using the same source revision. Build from the complete repository.

```sh
cd addons/ipmi
go test -race ./...
go vet ./...
GOOS=linux GOARCH=riscv64 CGO_ENABLED=0 go build -o /tmp/nkos-ipmi ./cmd/nkos-ipmi
```

From the repository root, `scripts/build-addon.py --addon nanokvm-ipmi`
builds the APK; use `--help` for toolchain and output options. The **Add-on
release** workflow validates the package on pull requests and publishes it
when manually dispatched. RustDesk uses its own builder.

The installed daemon remains `/usr/sbin/nkos-ipmi`, with OpenRC service
`nanokvm-ipmi` and shared settings in `/etc/kvm/bmc/config.json`.
