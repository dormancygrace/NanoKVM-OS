# Beta-9 release acceptance

Beta-9 sequence 22 booted on the development NanoKVM with a 64 MiB boot partition and 1488 MiB root partition. The existing data partition remained at sector 3180544. Boot has 56474 KiB free. Wi-Fi, the user-enabled SSH service, authenticated web API (nine endpoints), and restored USB settings were checked. Capture testing was not performed. Fresh-card data creation was not exercised; the controlled RAM flash preserved the existing p3 entry.

Only the complete SD image and SHA256SUMS are distributed for this release. A compact installer that creates filesystems and extracts files could avoid writing the free space embedded in the current raw image; that installer is not implemented in beta-9.
