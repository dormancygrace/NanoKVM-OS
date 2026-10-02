# Security policy

NanoKVM OS controls the keyboard, mouse, power and virtual media of the
computer it is attached to, so please report security problems privately.

## Reporting a vulnerability

Do not open a public issue. Use **Security → Report a vulnerability** on
this repository ([direct link](https://github.com/dormancygrace/NanoKVM-OS/security/advisories/new)).

Please include:

- the NanoKVM OS version (Settings → About) and board (Cube, Lite or PCIe);
- what an attacker needs (network position, account role, physical access);
- steps to reproduce and the impact you observed.

Remove passwords, tokens and private screen contents from logs and screenshots.

## Supported versions

Fixes are made for the latest published NanoKVM OS v2 release. The v1 betas
and the removed `.nkos` updater are no longer maintained; update through the
native APK packages or flash the current image.

## Scope

In scope: the server, web interface, init scripts, packaging and release
artifacts in this repository. Problems in upstream components (Sipeed
NanoKVM, SOPHGO SDK, Alpine packages, Pion, the Linux kernel) are best
reported upstream as well; we track and ship their fixes.

This is a community project; reports are handled on a best-effort basis.
