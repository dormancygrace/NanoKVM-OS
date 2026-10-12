# NanoKVM OS native support

`sg2002/` contains native capture/encoding and board-service source for NanoKVM Cube and PCIe. NanoKVM Pro and other SoC platforms are not included.

Use the pinned SDK sources and matched native build described in [BUILD.md](../docs/BUILD.md). The `nanokvm-app` APK updates the server, web assets, native media libraries and board service together; kernel and modules have separate packages. Source derives from Sipeed NanoKVM and retains its notices.
