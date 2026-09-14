# Beta-10 release acceptance

Beta-10 sequence 23 booted on the existing PCIe/UXC test device with Linux
7.2.5-nanokvm-os-r3. Wi-Fi connected at 5180 MHz and FQ-CoDel was active on WLAN
queues. The owner's SSH and USB settings were preserved. The installed APK
application `hello` 1.0.0-r0 was restored, and the package database, world and
provider contract were ready. Nine authenticated API checks passed. The owner
confirmed the video stream.

The first signed package reached the RAM installer, where add-on preflight
stopped before rootfs writing because the chroot lacked /dev/null. Binding /dev
allowed verification and installation to finish. The source now binds /dev into
both verification and restoration chroots, and the final signed archive was
regenerated. The final server adds versioned release discovery; it was installed
on the device and restarted successfully, with three authenticated status checks
passing afterward.

The regenerated final archive was not subjected to a second end-to-end install.
Fresh-card data creation and Realtek hardware were not exercised. Host checks
passed for the complete image layout, FAT/ext4, FIT payloads, all 92 matching
modules, dependency metadata and the full-system APK trust/provider contract.
The production web bundle passed 85 tests; the Go osupdate suite passed.
No additional OpenSSL testing was performed.

Beta-10 retains the legacy filenames so beta-9 can discover its package. Beta-11
and later use the versioned scheme documented in [release names](RELEASE-NAMING.md).
