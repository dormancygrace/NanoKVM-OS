# SG2002 native components

Source derives from Sipeed NanoKVM, retaining upstream notices.

- `kvm_system`: board monitoring, display/buttons and device services.
- `additional/kvm` and `additional/kvm_mmf`: capture, VPSS scaling and hardware encoding libraries used by the Go server.
- `nanokvm_update_edid`: the EDID programming tool.

The `native`, `system` and `tools` steps of `platform/build.sh` build them (`scripts/build-capture.py`, `build-mmf.py`, `build-system.py` and the EDID tool's Makefile); none needs MaixCDK.

For NanoKVM OS, use the pinned SDK versions, source patches and matched native build in [BUILD.md](../../docs/BUILD.md). Rebuild dependent native libraries together when the public ABI changes. The native libraries and board service are included in the `nanokvm-app` APK and are updated with the application. Kernel and modules are packaged separately.
