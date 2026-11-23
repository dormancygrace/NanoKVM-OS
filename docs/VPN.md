# VPN profiles

Settings → VPN contains WireGuard, OpenVPN, Tailscale, NetBird and ZeroTier submenus. Each page
shows its component version. WireGuard and OpenVPN each allow one enabled
profile; enabled profiles are restored after restart. Import alone does not
connect. Avoid overlapping routes when using multiple VPN clients.

## WireGuard

Upload one or more `.conf` files, then enable the profile you need. Existing profiles can be renamed without reconnecting.
**Route Allowed IPs** is off by default, independently of a supplied Table field.
AllowedIPs remains WireGuard peer selection/source validation; only connected
routes from Address are created. For example, Address `10.20.0.2/24` creates
the `10.20.0.0/24` route. A `/32` address does not create a subnet route.

To add routes from AllowedIPs, stop the profile and enable Route Allowed IPs.
`0.0.0.0/0` then routes ordinary IPv4 internet traffic through the VPN; `::/0`
does the same for IPv6. More-specific routes can still take precedence.
IPv6 interface addresses, or IPv6 AllowedIPs with routing enabled, require
IPv6 enabled in Network settings. Profile DNS applies to the device while active.
Executable hooks and SaveConfig are not accepted. No automatic rollback based
on missing handshakes is provided; Idle alone does not establish tunnel failure.

## OpenVPN

Upload `.ovpn` files with any referenced certificate/key files in the same
selection. The GUI runs Alpine's OpenVPN 2 client (`openvpn`), installed
with the system, and shows whether a tunnel uses the kernel's data-channel offload.
Enter requested username/password or private-key passphrase through the profile
controls. TUN and TAP profiles are supported; executable scripts and
interactive SSO/MFA are not. Server DNS applies to the whole device while
connected and is restored on disconnect. Arbitrary providers are not guaranteed
compatible.

A TAP profile puts the NanoKVM on the server's Ethernet segment without a
bridge: it takes the address and routes the server pushes (`server` or
`server-bridge` with an address pool); router advertisements and IPv6
autoconfiguration from that segment are ignored, and an address from DHCP
through the tunnel (`route-gateway dhcp`, `server-bridge` without a pool) is
not requested. The kernel's data-channel offload works with TUN only, so a
TAP tunnel is encrypted by OpenVPN itself and is several times slower.

## Tailscale

Open the Tailscale submenu to use its existing installation/login controls.

## ZeroTier

The ZeroTier submenu installs the optional `nanokvm-zerotier` add-on from the
NanoKVM repository and then offers to start the `zerotier-one` service;
starting it also adds it to the boot runlevel, and stopping it takes it out.
Once it runs, the submenu shows the node ID, whether the node has reached
ZeroTier's root servers, and the joined networks with their state and
addresses. Join a network with its 16-digit ID, then authorize the node ID in
that network's controller (ZeroTier Central or your own); until then the
network shows **Not authorized**. Leave removes the network from this device
only. ZeroTier can run alongside the other VPNs; avoid overlapping routes.

The add-on is ZeroTier One's agent (MPL-2.0), built from ZeroTier's release
for musl and the C906, without ZeroTier's network controller, which is
source-available and not part of the package. A controller is not needed
to join networks. The identity and joined networks live in
`/var/lib/zerotier-one`, the same place as other ZeroTier installations, so
an identity copied there before starting keeps its node ID. They are kept on
uninstall unless you choose to delete them, and the settings backup includes
them for **Restore this device**, never for migration: two devices with one
identity disrupt each other. A backup taken before ZeroTier first started
holds only its `local.conf`, not network files preseeded in `networks.d`;
restoring it keeps the current identity and networks. Managed routes are
applied; default-route and DNS settings pushed by a network are not
(ZeroTier does not set DNS on Linux).
