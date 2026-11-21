# NanoKVM OS documentation

## Installing and updating

- [Installing NanoKVM OS](INSTALL.md): SD image, card layout, write policy
- [Migrating stock Sipeed firmware](STOCK-INSTALL.md): replace the stock system on the same card
- [Package updates](UPDATES.md): system and application updates
- [Software packages](SOFTWARE-PACKAGE-UPDATES.md): package indexes and packages released on their own
- [Settings backup, migration and factory reset](SETTINGS-RECOVERY.md)
- [Boot chain, console and recovery](BOOT.md)
- [Release naming](RELEASE-NAMING.md)

## Video and accelerators

- [Video and HDMI](VIDEO.md): transports, codecs, monitor profiles, MJPEG, limits, API
- [Accelerators](ACCELERATORS.md): IVE, TPU, DWA/RGN, random numbers, CryptoDMA, small-core audio

## USB and virtual media

- [USB composition](USB-COMPOSITION.md): gadget functions, endpoint budget, host serial console
- [Internet over USB](USB-INTERNET-SHARING.md)
- [USB Serial resize bridge](USB-SERIAL.md)
- [Image transfer and ISO mounting](STORAGE-TRANSFER-SAFETY.md): browser, HTTP(S) and SMB sources
- [Network boot (netboot.xyz)](NETBOOT.md)

## Network and access

- [Network changes on trial](NETWORK-TRIAL.md)
- [Finding NanoKVMs on the network](DISCOVERY.md): the service every device announces, and the list of the others
- [Ethernet interrupt coalescing](ETHERNET-RX-BATCHING.md)
- [VPN profiles](VPN.md)
- [Two-factor authentication](TWO-FACTOR.md)
- [Sign-out after inactivity](IDLE-SIGNOUT.md)
- [SSH public keys](SSH-KEYS.md)

## Managing the computer

- [Overview page](OVERVIEW.md): the start page with the state of the device and a preview of the screen
- [Redfish and IPMI](BMC.md)
- [Computer sensors](HOST-SENSORS.md)
- [LLDP, SNMP and UPS](NETWORK-SERVICES.md)
- [ATX LED display](LEDS.md)
- [NanoKVM shutdown with network restart](SAFE-POWEROFF.md)
- [Event log](EVENTS.md) and [logs](LOGS.md)
- [Scheduled tasks](SCHEDULE.md) and [scripts](SCRIPTS.md)

## Building and maintaining

- [Building NanoKVM OS](BUILD.md)
- [Hot kernel text](KERNEL-HOT-TEXT.md)
- [Component build profiles](COMPONENT-BUILD-PROFILES.md)
- [Dependency maintenance](DEPENDENCY-UPDATES.md)
- [Third-party software](THIRD-PARTY.md) and [source distribution](DISTRIBUTION.md)
