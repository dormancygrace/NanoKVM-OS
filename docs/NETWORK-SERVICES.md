# LLDP, SNMP and UPS

Settings → Services has three optional services. None is part of the image:
each page starts with an **Install** button that installs the Alpine package
(the install turns on Alpine's community repository). An installed service
has a switch; its settings appear while it is on. **Uninstall** stops the
service and removes its packages; the configuration stays and is used again
after a new install.

| Service | Package | Listens on |
| --- | --- | --- |
| LLDP | `lldpd` | nothing (link-layer frames) |
| SNMP | `net-snmp` | UDP 161 |
| UPS | `nut` | TCP 3493, the local host only unless shared |

The pages write the services' own configuration files. A file written from
the settings starts with a comment saying so, and is replaced whenever the
settings are saved.

## LLDP

`lldpd` announces the NanoKVM (host name, address, port) to the switch it is
plugged into, and the page lists the neighbours that announce themselves:
name, protocol, interface, their port and management address.

**CDP** also listens for the Cisco Discovery Protocol and answers devices that
sent it (`lldpd -c`, in `/etc/conf.d/lldpd`).

## SNMP

The `net-snmp` agent answers read-only SNMP v2c requests about the NanoKVM
itself: system, interfaces and addresses, uptime, storage, processors, load and
memory. The rest of the tree is not served; the process table, which shows
command lines, is part of it.

- **Community**: generated at the installation, 16 characters; it can be
  replaced by one of 8 to 32 letters, digits, dots, dashes or underscores.
  There is no `public` community.
- **Allowed source**: an IPv4 address or network (`192.168.1.0/24`) that may
  ask. Empty allows anyone who knows the community. The agent listens on IPv4.
- **Location** and **Contact**: `sysLocation` and `sysContact`.

The configuration is `/etc/snmp/snmpd.conf`, readable by root only.

## UPS

Network UPS Tools reads a UPS attached to the NanoKVM and shares its state
with the computers the UPS feeds. Those computers run their own NUT client
(`upsmon`, or an operating system's NUT support) and shut down on its
decision; the NanoKVM does nothing on a power failure.

- **Connection**: USB or a serial port.
  - **USB** needs the USB port in the host mode
    ([USB composition](USB-COMPOSITION.md)); the page says so when it is not.
    It lists what is attached: **Any UPS the driver finds**, or one device,
    which the driver then takes by its vendor and product ID.
  - **Serial port** offers the two ports of the NanoKVM's header,
    `/dev/ttyS1` and `/dev/ttyS2`. The port must not be in use by the serial
    terminal or the computer sensors.
- **Driver**: the drivers of the chosen connection. `usbhid-ups` serves most
  USB UPSes; a serial UPS takes the driver of its protocol.
- **Share on the network**: off, the UPS is read on the NanoKVM only. On,
  `upsd` listens on TCP port 3493 and the page shows what a client needs: the
  UPS name `ups@<address>`, the user `monitor` and its generated password
  (a secondary `upsmon` account). **New password** replaces the password.
- **State**: what the UPS reports (`upsc`), the main readings first.

The service can be on and not running: without a UPS, or with a driver that
does not fit it, the driver does not start. **Start** on the page tries again,
for example after attaching the UPS. The configuration is in
`/etc/nut` (`nut.conf`, `ups.conf`, `upsd.conf`, `upsd.users`).
