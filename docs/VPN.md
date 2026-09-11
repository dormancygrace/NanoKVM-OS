# VPN profiles

Settings → VPN contains WireGuard, OpenVPN and Tailscale submenus. Each page
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
selection. The GUI uses OpenVPN 3 Core 3.12-dev pinned at
`87fd40bb583cc89787c0bf4da79744f0e6d02ac3`, with the upstream Linux `ovpn` DCO ABI.
Enter requested username/password or private-key passphrase through the profile
controls. Routed TUN profiles are supported; TAP, executable scripts and
interactive SSO/MFA are not. Server DNS applies to the whole device while
connected and is restored on disconnect. Arbitrary providers are not guaranteed
compatible. The system OpenVPN 2 command-line tool is separate from the GUI.

## Tailscale

Open the Tailscale submenu to use its existing installation/login controls.
Tailscale account login was not part of this release's VPN validation.
