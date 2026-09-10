# SG2002 native components

Source derives from Sipeed NanoKVM and the MaixCDK integration, retaining upstream notices.

- `kvm_system`: board monitoring, display/buttons and device services.
- `additional/kvm` and `additional/kvm_mmf`: capture, VPSS scaling and hardware encoding libraries used by the Go server.
- The local `build` helper supports MaixCDK development. Its generic defaults are not a release-equivalent toolchain/native dependency set.

For NanoKVM OS, use the pinned SDK versions, source patches and matched native build in [BUILD.md](../../docs/BUILD.md). Rebuild dependent native libraries together when the public ABI changes. Native components belong to the full system image and are not replaced by application-only updates.
