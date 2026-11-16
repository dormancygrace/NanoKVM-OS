# Internet over USB NCM

The USB settings page has an independent **Internet over USB** switch
(**Интернет через USB** in Russian). It defaults to off. NCM alone continues
providing the computer's USB address, without a DHCP default gateway or DNS
server; what the computer may open on the NanoKVM over it is described in
[What the computer reaches over the USB network](USB-COMPOSITION.md#what-the-computer-reaches-over-the-usb-network). Sharing
uses IPv4 only; it does not enable IPv6 forwarding, RA or DHCPv6. IPv6 transit
through usb0 is explicitly dropped.

## Enable

1. The Alpine base package must provide `/usr/sbin/nkos-usb-internet`, its
   S30usbnet hooks and the OpenRC watcher, matching the application package.
2. In Settings → USB, enable USB network (NCM) and apply the composition.
3. Turn on Internet over USB. This does not rebind the USB gadget.
4. Let the managed computer obtain an address through DHCP. If its old lease
   does not renew promptly, reconnect USB or renew DHCP on that computer;
   NanoKVM does not change its network configuration.

The UI shows the chosen gateway/interface, waiting for NCM/uplink, explicitly
disabled DHCP, subnet conflict or service failure. “Active” means sharing is
configured via a live Ethernet/Wi-Fi route; it does not assert upstream Internet
reachability. Loss of Internet beyond an otherwise live gateway is therefore
not displayed as a separate verified connectivity state.

Turning NCM/USB off pauses sharing while preserving its preference. Restoring
NCM resumes sharing. Turning the sharing switch off keeps the USB network
with its address and DHCP.
DHCP leases last 120 seconds in both modes; clients may retain previous DHCP
options until renewing.

## System ownership and behavior

- `/etc/kvm/usb/usb.internet` persists the independent preference. Only the admin
  API can change it. Missing NCM/uplink is a saved waiting state, not an error.
- S03usbdev owns the gadget. Its S30usbnet start/stop hooks call the Alpine
  helper under the USB lock. The script lives in
  `firmware/alpine/compat/S30usbnet`.
- The helper owns one usb0 IPv4 address and one isolated dnsmasq process.
  While [network boot](NETBOOT.md) is on, `netboot.port` in the runtime
  directory makes dnsmasq hand iPXE clients (DHCP option 175 only) the boot
  script on `<usb0 address>:<port>`; other clients never get a boot file.
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
- NAT, the software flowtable, USB-only filtering and the usb0 input filter
  ([what the computer reaches](USB-COMPOSITION.md#what-the-computer-reaches-over-the-usb-network))
  live in `inet nkos_usb_internet`, identified by an owner comment. Replacement
  is one nft transaction; foreign tables are never flushed. Off/waiting retains
  only the USB transit guard and the input filter, so globally enabled VPN
  forwarding cannot accidentally share Internet. Without an address of the
  helper (`rndis.nodhcpd`, a subnet conflict or an error before the first
  address) a bound usb0 keeps the input filter alone. Gadget stop removes the
  whole owned table, daemon and address.
- Software offload is eligible for established return-direction IPv4 TCP/UDP
  with zero conntrack mark and only default policy routing. Foreign transit or
  ingress hooks suppress it. The exact package SYN-only `nkos_mss` ceiling is
  recognized as compatible; custom/additional rules in that table suppress
  offload. No `flags offload` or hardware acceleration claim is made. Kernel
  rejection of the flowtable falls back to ordinary NAT and is shown in the UI.
- There is no generic flow offload table: acceleration is USB-scoped. A
  leftover wildcard `nkos_flow` table is deleted on package upgrade;
  `nkos_mss` is kept.
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
`offloadReason`, `ipv6: false`, `hostAccess` (the computer may open every
service over USB, `PUT /api/vm/usb-host-access`) and timestamp. States: disabled, active,
waiting-ncm, waiting-uplink, dhcp-disabled, subnet-conflict, unavailable, error.
Status older than twenty seconds is considered unavailable.

## Limits

On `usb0` the gadget driver rejects TSO, scatter-gather, checksum offload and
hardware GRO, and its receive path uses `netif_rx`; software GRO gives no
speed gain.

## References

- [Linux flowtable behavior and limitations](https://docs.kernel.org/networking/nf_flowtable.html)
- [Linux forwarding sysctls](https://docs.kernel.org/networking/ip-sysctl.html)
- [dnsmasq configuration and DHCP options](https://thekelleys.org.uk/dnsmasq/docs/dnsmasq-man.html)
