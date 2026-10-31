# NanoKVM OS native support

Native capture, encoding and board-service source for NanoKVM Cube and PCIe (SG2002). NanoKVM Pro and other SoC platforms are not included. Source derives from Sipeed NanoKVM and retains its notices.

- `kvm_system`: board monitoring, display/buttons and device services.
- `additional/kvm` and `additional/kvm_mmf`: capture, VPSS scaling and hardware encoding libraries used by the Go server.
- `nanokvm_update_edid`: the EDID programming tool.

The `native`, `system` and `tools` steps of `platform/build.sh` build them (`scripts/build-capture.py`, `build-mmf.py`, `build-system.py` and the EDID tool's Makefile); none needs MaixCDK.

Use the pinned SDK sources, source patches and matched native build described in [BUILD.md](../docs/BUILD.md). Rebuild dependent native libraries together when the public ABI changes. The `nanokvm` APK updates the server, native media libraries and board service together; kernel and modules have separate packages.
