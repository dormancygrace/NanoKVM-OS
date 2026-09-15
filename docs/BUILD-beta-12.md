# Building beta-12 sequence 28

Version `1.0.0-beta.12`, tag `v1.0.0-beta.12`. Full SD image only.
Exact input identities and image hash are in `firmware/release/beta12/build-manifest.json`.

This release reuses the accepted beta-11 seq27 server, Web, capture library,
OpenSSL 4.0.2, -O2 userspace, kernel `7.2.5-nanokvm-os-r3`, 92 modules,
FIT and bootloader. See [beta-11 build prerequisites](BUILD-beta-11.md).
The changed S15kvmhwd is installed in both the live init directory and the
application restore template. No kernel or application rebuild is required.

As root, run `firmware/release/beta12/stage-rootfs.sh` with
`NANOKVM_BUILD_BASE` pointing to the accepted inputs. After unmounting, give
the build user ownership of the output. Run `build-artifacts.sh` with
`NANOKVM_BUILD_BASE`, `NANOKVM_RELEASE_OUTPUT` and `SOURCE_DATE_EPOCH` from
the manifest. Input locations follow the beta-11 recipe; these scripts are
not a turnkey downloader for missing external prerequisites.

Outputs are the full image ZIP, SHA256SUMS and build manifest. No package is
signed. See [acceptance](BETA12-ACCEPTANCE.md) and [distribution status](DISTRIBUTION.md).
