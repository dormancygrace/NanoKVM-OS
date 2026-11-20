# LLDP, SNMP and UPS

Settings → Services has three network services. Their Alpine packages are
installed with the system and kept installed; a device updated from a release
without them gets them shortly after its first start (`lldpd` and `nut` come
from Alpine `community`, `net-snmp` from `main`). Each service is off until
it is switched on: none is started at boot before that. Each page has a
switch; the settings appear while the service is on. A page whose package is
not installed, not yet or because it was removed on the Software page, says
so.

| Service | Package | Listens on |
| --- | --- | --- |
| LLDP | `lldpd` | nothing (link-layer frames) |
| SNMP | `net-snmp` | UDP 161 |
| UPS | `nut` | TCP 3493, the local host only unless shared |

The pages write the services' own configuration files. A file written from
the settings starts with a comment saying so, and is replaced whenever the
settings are saved. The package's own example file is replaced at the first
use.

A configuration file that is neither, because somebody wrote or edited it by
hand, is never written over: the service runs with it as it is, the page says
so and shows no settings. **Replace** on that page writes the configuration
of a fresh installation in its place; the previous file is not kept.

A change is applied to a stopped service. When the service does not stop, the
change (switching off, settings) is refused and nothing is written.

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

- **Community**: generated when the service is first switched on, 16
  characters; it can be
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
