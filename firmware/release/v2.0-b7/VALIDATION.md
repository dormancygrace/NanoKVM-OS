# NanoKVM OS v2.0-b7 validation

Prepared 2026-10-02. Integrates public PRs #9 through #14. Published as GitHub release v2.0-b7 on 2026-10-02.

## Build and automated checks

Full integrated Go race/teststub and vet checks, frontend tests, TypeScript and production web build passed. Server, web and APK helpers rebuilt for b7. Kernel and native capture libraries retained from b6; Linux 7.2.6-nanokvm-os-r1.

Signed APK upgrade in an isolated b6-r1 root passed with real package scripts. Installed server matches the build; installed helpers match signed package contents (abuild strips helper executables). Obsolete updater init scripts absent. Full SD image assembly passed. Artifacts are described in SHA256SUMS and build-manifest.json.

## Device test

Installed signed nanokvm-base, nanokvm-app and nanokvm-release 2.0_beta7-r0 through native apk with a temporary local signed repository. Package pins for those three packages were released after installation. Existing kernel pin was left unchanged. Server settings, credential file and hostname hashes were unchanged by package installation; subsequently the user changed the factory password through the web interface.

Factory-password change screen appeared as required; the user completed it and logged in. Chrome Main showed 2.0-b7, QHD 2560x1440 H.264 Direct with output observed up to 49/50 FPS, Wi-Fi and WireGuard connected, and zram active. These are smoke observations, not benchmark guarantees.

Normal reboot completed. Boot ID changed from 419b045c-ea14-4fa1-b6aa-fcf01c492cea to f81dae2e-214e-44b0-a270-77f68f118749. App, network and watchdog started; rc-status --crashed returned no crashed services. Hostname hash remained unchanged. Existing SSH key continued working after user password change. Browser reconnected without signing in again and displayed the live HDMI source. Updates page displayed installed 2.0-b7 and the running kernel. A second Chrome Main tab connected and the UI reported two active video sessions; the test tab was then closed. USB gadget remained off throughout.

No backups created. Build logs and screenshots are kept outside the repository. No power actions were sent to the controlled host. Physical input ownership/ATX behavior, HTTPS off/on cycling, fresh-card boot and long-duration soak were not hardware-tested in this pass. Related automated test results do not imply those hardware scenarios were exercised.

## Release status

Full image ZIP: 139653598 bytes (133.2 MiB); raw image: 872415744 bytes. Three update APKs total 17544420 bytes (16.7 MiB). Published as GitHub release v2.0-b7; asset digests match SHA256SUMS.
