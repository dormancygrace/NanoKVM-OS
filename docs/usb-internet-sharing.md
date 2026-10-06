# Internet over USB NCM

The USB settings page has an independent **Internet over USB** switch
(**Интернет через USB** in Russian). It defaults to off. NCM alone continues
providing local access without a DHCP default gateway or DNS server. Sharing
uses IPv4 only; it does not enable IPv6 forwarding, RA or DHCPv6. IPv6 transit
through usb0 is explicitly dropped.

## Enable

1. Install matching updated Alpine base and application packages. The base must
   contain `/usr/sbin/nkos-usb-internet`, the updated S30usbnet and OpenRC watcher.
2. In Settings → USB, enable USB network (NCM) and apply the composition.
3. Turn on Internet over USB. This does not rebind the USB gadget.
4. Let the managed computer obtain an address through DHCP. If its old lease
   does not renew promptly, reconnect USB or renew DHCP yourself. Codex has not
   modified the managed Windows network configuration.

The UI shows the chosen gateway/interface, waiting for NCM/uplink, explicitly
disabled DHCP, subnet conflict or service failure. “Active” means sharing is
configured via a live Ethernet/Wi-Fi route; it does not assert upstream Internet
reachability. Loss of Internet beyond an otherwise live gateway is therefore
not displayed as a separate verified connectivity state.

Turning NCM/USB off pauses sharing while preserving its preference. Restoring
NCM resumes sharing. Turning the sharing switch off retains local USB access.
DHCP leases last 120 seconds in both modes; clients may retain previous DHCP
options until renewing.

## System ownership and behavior

- `/boot/usb.internet` persists the independent preference. Only the admin
  API can change it. Missing NCM/uplink is a saved waiting state, not an error.
- Existing S03usbdev still owns the sole gadget implementation. Its existing
  S30usbnet start/stop hooks call the new Alpine helper under the existing lock.
  The Alpine compatibility script lives in `firmware/alpine/compat/S30usbnet`.
  Legacy Buildroot sources remain unchanged; this feature ships in Alpine packages.
- The helper owns one usb0 IPv4 address and one isolated dnsmasq process.
  DNS listens only on the USB address/interface while active. No other DNS or
  DHCP process is stopped. An existing wildcard DNS listener can prevent
  activation; the API rolls back the switch on a runtime error.
- The original UID-derived /24 is preferred. Connected/nondefault IPv4 routes
  in all routing tables are checked. Automatic conflicts select a deterministic
  unused private /24; the selected address survives helper reapplication and
  gadget rebind within the current boot. An explicit `rndis.ipv4_prefix` is
  never silently replaced. A conflict pauses NAT/gateway advertisement and
  preserves an already-owned local address. Existing `rndis.nodhcpd` remains
  authoritative and pauses this managed sharing mode.
- A forwarded route lookup includes USB source and ingress. Only eth0/wlan0
  with carrier qualify. A VPN-selected route is not bypassed by installing a
  separate routing table. Consequently sharing may wait while a full-tunnel
  VPN owns the route. Device VPN configuration and management routes are not
  changed. Client-specific policy routes remain subject to the ordinary kernel
  routing and other firewall chains.
- Only per-interface IPv4 forwarding on usb0 and the selected uplink is changed;
  the helper never writes global `ip_forward` (which resets other IP settings).
  Values it changed are journaled under `/run/nanokvm-usbnet`. On stop/interface
  change they are restored if still owned and no foreign transit policy has
  appeared. If another transit policy now exists, forwarding is conservatively
  retained for that policy; USB transit remains blocked by the local-only guard.
- NAT, the software flowtable and USB-only filtering live in
  `inet nkos_usb_internet`, identified by an owner comment. Replacement is one
  nft transaction; foreign tables are never flushed. Off/waiting retains only
  the USB transit guard, so globally enabled VPN forwarding cannot accidentally
  share Internet. Gadget stop removes the whole owned table, daemon and address.
- Software offload is eligible for established return-direction IPv4 TCP/UDP
  with zero conntrack mark and only default policy routing. Foreign transit or
  ingress hooks suppress it. The exact package SYN-only `nkos_mss` ceiling is
  recognized as compatible; custom/additional rules in that table suppress
  offload. No `flags offload` or hardware acceleration claim is made. Kernel
  rejection of the flowtable falls back to ordinary NAT and is shown in the UI.
- S35flowoffload now retires the earlier package-owned wildcard `nkos_flow`;
  its broad cached flows could otherwise bypass the USB guard and VPN policy.
  The matching base upgrade is required. This replaces the generic acceleration
  policy with USB-scoped acceleration. `nkos_mss` is retained.
- OpenRC `nanokvm-usb-internet` watches link/address/route/rule and foreign nft
  changes, with a five-second fallback reconciliation. Gateway/interface changes
  replace the table and discard its cached flows. Connections using a previous
  address/uplink may need to reconnect. This is not seamless connection migration.
- Runtime errors retire stale gateway/NAT state, preserving local access where
  possible. PID ownership is verified against the executable and exact config
  argument. Boot preference and applied runtime journal use atomic file writes.

## API

Admin-authenticated GET/PUT `/api/vm/usb-internet`.

```json
{"enabled": true, "expectedEnabled": false}
```

Both booleans must be supplied, including an explicit false. The expected value
prevents a stale client from silently overwriting a different saved setting.
The API serializes with USB composition changes. A real runtime failure restores
the previous preference and reapplies it; waiting states are successful saves.
A lost response is handled by read-back in the GUI rather than mutation replay.

The response includes `enabled`, `state`, `address`, `uplink`, `flowOffload`,
`offloadReason`, `ipv6: false` and timestamp. States: disabled, active,
waiting-ncm, waiting-uplink, dhcp-disabled, subnet-conflict, unavailable, error.
Status older than twenty seconds is considered unavailable.

## Verification

- Go tests with native `teststub`: helper planning/ownership/policy, VM
  persistence/rollback, existing USB composition, proto and router.
- Actual Linux namespace qualification: local/active DHCP options, gateway/DNS,
  DNS query, NAT ping, no uplink, repeated application without DHCP/rule restart,
  start/stop preference preservation, local access after disable, foreign policy
  preservation, Ethernet/Wi-Fi switch and link loss, VPN route selection, custom
  subnet conflict and automatic alternative, NCM removal, event-driven watcher.
- Prefix validation, Alpine service dependency/failure checks and package payload
  checks, including new helper/service and canonical S30usbnet.
- Production TypeScript/Vite build, changed-file ESLint, USB composition tests.
- Chrome **Main**: local preview of the real USB settings components with modeled
  API responses; independent enable/disable and waiting states. This is GUI
  qualification, not a device/Windows end-to-end test.

Namespace test command (root only; uses disposable namespaces, never the device):

```sh
cd server
go build -o ../work/nkos-usb-internet ./cmd/nkos-usb-internet
cd ..
sudo python3 scripts/test-usb-internet-netns.py
```

The WSL kernel lacks flowtable support; its tests cover normal-forwarding fallback.
Hardware qualification on SG2002 and a physical Windows NCM host was completed
on 2026-10-05 using matching temporarily staged base components and application:

- Real Chrome Main GUI enable/disable, local DHCP without gateway/DNS, actual
  Wi-Fi NAT, gateway DNS, direct DNS and certificate-verified HTTPS passed.
- Software flowtable datapath was demonstrated with matched forward counters:
  20 UDP DNS queries counted as 1 forwarded packet with flowtable versus 20
  without; 10 keep-alive HTTPS requests counted as 4 versus 30 TCP packets.
  NF_CT_NETLINK is unavailable, so conntrack OFFLOAD tags could not be queried.
- Foreign-forward policy suppression, USB source-specific WireGuard route
  waiting, logical gadget detach/rebind and reboot persistence passed.
- On usb0, TSO/SG/checksum/hardware GRO requests are rejected; GSO remains
  effectively off despite a requested-on flag. Software GRO toggles and 12
  short local TCP on/off runs passed without increasing error/drop counters.
  The current u_ether RX path uses netif_rx rather than GRO. A reliable GRO
  speed improvement was not established.
- Actual Ethernet uplink (carrier 0), physical cable unplug, real Wi-Fi uplink
  removal and a controlled NAT throughput/CPU comparison remain untested in
  this window. Namespace link/uplink-switch tests remain valid.
- Original application/web/S30/S35 files were restored (192 matching SHA-256
  hashes), along with the exact original nft rules. NCM and sharing were left
  off; HID/audio and RustDesk were left on under the user's latest instruction.
  All rollback captures stayed on the host, with no device/SD backups.

Detailed host evidence is under work/hardware-20261005; the Windows task's
outputs/hardware-test-results.md contains the user-facing hardware report.
Do not alter Windows networking automatically, retain device/SD backups,
publish an OS release or merge this branch as part of this work.

## References

- [Linux flowtable behavior and limitations](https://docs.kernel.org/networking/nf_flowtable.html)
- [Linux forwarding sysctls](https://docs.kernel.org/networking/ip-sysctl.html)
- [dnsmasq configuration and DHCP options](https://thekelleys.org.uk/dnsmasq/docs/dnsmasq-man.html)

## v2.5-a1 integration

The platform package manifest explicitly includes the helper, OpenRC watcher and
Alpine S30usbnet in both compatibility locations. The rootfs build rejects missing
helper/service files. This adapts commits 143deaa and e7a2d61 to the current build
without changing toolchain inputs or installing an Alpine helper into legacy
Buildroot images.
