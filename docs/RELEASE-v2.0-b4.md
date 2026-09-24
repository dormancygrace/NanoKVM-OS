# 🛠️ NanoKVM OS v2.0 b4 — Multimedia Initialization Dependency

This maintenance release supplies a missing command used by the Sophgo multimedia libraries during initialization.

## 🎬 Multimedia initialization

- Added a dedicated **BusyBox `devmem`** executable to the base package. It provides the `devmem ADDRESS [WIDTH [VALUE]]` syntax expected by the multimedia stack.
- Fixes the missing userspace dependency behind **`sh: devmem: not found`** messages during initialization.
- Alpine's system BusyBox is unchanged. The added executable contains only the required applet and is built for RISC-V with `-O2`.

## 🧪 LT6911D investigation

This dependency was identified while investigating **issue #8**, which reports video initialization failures on NanoKVM PCIe with an LT6911D HDMI receiver.

**Restoring `devmem` is not yet a confirmed fix for the LT6911D video failure.** The available hardware is LT6911UXC; the command's syntax and execution were checked without reading or writing physical memory. Validation on the affected LT6911D hardware is still needed.

## ⬆️ Updating from v2.0 a2, b1, b2 or b3

**Update directly — no intermediate release or SD-card reflash is required.** Use **Package updates** in Settings, or run:

```sh
apk update
apk upgrade
```

Then reboot so the multimedia stack initializes with the dependency available. Settings, user data and independently installed packages are retained.

Linux remains **`7.2.6-nanokvm-os-r1`**. The kernel and multimedia libraries are unchanged from b3.

## 💾 Fresh installation

Use **`NanoKVM-OS-v2.0-b4.img.zip`** and verify it with **`SHA256SUMS`**. Existing v2 installations can use the package update above.

**From beta-14 or an older beta, use the full image. Flashing replaces the installation and data.**
