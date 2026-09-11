# Buildroot release source patches

Apply 0001-json-c-0.19.patch to a clean Buildroot 2026.08 source archive
before configuring nanokvm_enhanced_defconfig. This aligns json-c with the MPI
build. Official release hash: https://github.com/json-c/json-c/wiki

Archive SHA256: 87aaca4164ea9d5c8085854953018263f7963f07c22e73a2a2185cc98c581c34.
Use separate source and output directories for a clean build. Image assembly checks and runtime limits are summarized in [validation](../../../docs/VALIDATION.md). Package selection does not establish Bluetooth hardware operation.

Apply every numbered patch in filename order, as scripts/build-enhanced.sh does.
0003-openvpn-2.7.7.patch updates the base OpenVPN 2.7.6 package to 2.7.7 for beta.
Its archive hash comes from the official GitHub v2.7.7 release asset digest;
the fetched Buildroot-mirror archive matches it. COPYRIGHT.GPL is unchanged.
The rebuilt target retains ENABLE_DCO=1. No running VPN profile is installed.

0004-chrony-4.9.patch selects the chrony release used by the Date & Time service.
0005-nano-9.2.patch selects nano 9.2. Apply all numbered patches in filename order.
