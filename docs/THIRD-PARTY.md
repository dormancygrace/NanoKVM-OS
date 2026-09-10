# Third-party software

This is a derivative firmware project, not an original implementation of every component.

- NanoKVM application/native code: Sipeed and contributors, GPL-3.0; see root `LICENSE` and retained source headers.
- Linux and applicable drivers: their upstream licenses, including GPL-2.0; kernel patches retain source notices.
- Pion SRTP local adaptation: MIT, with the upstream license in `server/third_party/pion-srtp/LICENSE`. Other Go modules remain pinned in `go.mod`/`go.sum` and retain their own licenses.
- Web dependencies retain their package licenses; versions are pinned in `web/pnpm-lock.yaml`.
- Go runtime additions retain the Go license in `firmware/cpu/sysmon-runtime/LICENSE`.
- SOPHGO/CVITEK MPI, ISP, drivers and sensor dependencies have component-specific terms. Some ISP/3A algorithm objects remain vendor supplied; this repository does not claim a completely open-source replacement for those objects.
- OpenVPN, Tailscale, Buildroot packages, Wi-Fi firmware and toolchain components retain their individual licenses. Building or distributing a full image requires corresponding source and firmware notices beyond the application license.

The repository is not yet a complete corresponding-source distribution for every full-image component. The collected target-package license texts and manifest are under `firmware/release/licenses/`. Missing vendor firmware terms and external build inputs are listed in [distribution status](DISTRIBUTION.md). Preserve upstream copyrights and attribution; the application license does not grant rights over every included firmware blob.
