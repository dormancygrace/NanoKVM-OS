# Functional parity matrix

204 baseline registrations: 22 implemented in the isolated Rust slice, 1 partial, 181 pending. This is source/host qualification, not complete hardware parity.

Owner OS password synchronization cannot run in an isolated root; its rollback path is tested. Internal-token/MCP routes fail closed. Pending public/session/admin routes return HTTP 501 after their access gate. All nonpublic baseline routes have an unauthenticated protection test.

| Method | Path | Access | Go handler/source | Rust status | Evidence |
|---|---|---|---|---|---|
| GET | `/api/addons/inventory` | admin | server/router/addons.go:15 | pending | — |
| POST | `/api/auth/login` | public | server/router/auth.go:14 | implemented-isolated | validation.md; API/contract/UI slice |
| GET | `/api/auth/password` | session | server/router/auth.go:18 | implemented-isolated | validation.md; API/contract/UI slice |
| GET | `/api/auth/account` | session | server/router/auth.go:19 | implemented-isolated | validation.md; API/contract/UI slice |
| POST | `/api/auth/password` | session | server/router/auth.go:20 | implemented-isolated | validation.md; API/contract/UI slice |
| POST | `/api/auth/logout` | session | server/router/auth.go:21 | implemented-isolated | validation.md; API/contract/UI slice |
| GET | `/api/auth/users` | admin | server/router/auth.go:27 | implemented-isolated | validation.md; API/contract/UI slice |
| POST | `/api/auth/users` | admin | server/router/auth.go:28 | implemented-isolated | validation.md; API/contract/UI slice |
| PUT | `/api/auth/users/:username` | admin | server/router/auth.go:29 | implemented-isolated | validation.md; API/contract/UI slice |
| DELETE | `/api/auth/users/:username` | admin | server/router/auth.go:30 | implemented-isolated | validation.md; API/contract/UI slice |
| POST | `/api/auth/users/:username/password` | admin | server/router/auth.go:31 | implemented-isolated | validation.md; API/contract/UI slice |
| GET | `/api/branding` | public | server/router/branding.go:13 | implemented-isolated | validation.md; API/contract/UI slice |
| GET | `/api/branding/logo` | public | server/router/branding.go:14 | implemented-isolated | validation.md; API/contract/UI slice |
| GET | `/api/branding/favicon` | public | server/router/branding.go:15 | implemented-isolated | validation.md; API/contract/UI slice |
| POST | `/api/branding/logo` | admin | server/router/branding.go:17 | pending | — |
| DELETE | `/api/branding/logo` | admin | server/router/branding.go:18 | pending | — |
| POST | `/api/branding/favicon` | admin | server/router/branding.go:19 | pending | — |
| DELETE | `/api/branding/favicon` | admin | server/router/branding.go:20 | pending | — |
| POST | `/api/branding/button-color` | admin | server/router/branding.go:21 | pending | — |
| DELETE | `/api/branding/button-color` | admin | server/router/branding.go:22 | pending | — |
| POST | `/api/branding/banner-style` | admin | server/router/branding.go:23 | pending | — |
| GET | `/api/ai/control/status` | admin | server/router/control.go:27 | pending | — |
| PUT | `/api/ai/control/mode` | admin | server/router/control.go:36 | pending | — |
| POST | `/api/download/image` | admin | server/router/download.go:15 | pending | — |
| POST | `/api/download/image/cancel` | admin | server/router/download.go:16 | pending | — |
| GET | `/api/download/image/status` | admin | server/router/download.go:17 | pending | — |
| GET | `/api/download/image/enabled` | admin | server/router/download.go:18 | pending | — |
| POST | `/api/download/file` | admin | server/router/download.go:19 | pending | — |
| GET | `/api/extensions/vpn/versions` | admin | server/router/extensions.go:21 | pending | — |
| POST | `/api/extensions/openvpn/install` | admin | server/router/extensions.go:23 | pending | — |
| POST | `/api/extensions/openvpn/uninstall` | admin | server/router/extensions.go:24 | pending | — |
| GET | `/api/extensions/openvpn/status` | admin | server/router/extensions.go:25 | pending | — |
| POST | `/api/extensions/openvpn/import` | admin | server/router/extensions.go:26 | pending | — |
| POST | `/api/extensions/openvpn/profile` | admin | server/router/extensions.go:27 | pending | — |
| GET | `/api/extensions/wireguard/status` | admin | server/router/extensions.go:29 | pending | — |
| POST | `/api/extensions/wireguard/import` | admin | server/router/extensions.go:30 | pending | — |
| POST | `/api/extensions/wireguard/profile` | admin | server/router/extensions.go:31 | pending | — |
| POST | `/api/extensions/tailscale/install` | admin | server/router/extensions.go:35 | pending | — |
| POST | `/api/extensions/tailscale/uninstall` | admin | server/router/extensions.go:36 | pending | — |
| GET | `/api/extensions/tailscale/status` | admin | server/router/extensions.go:37 | pending | — |
| POST | `/api/extensions/tailscale/up` | admin | server/router/extensions.go:38 | pending | — |
| POST | `/api/extensions/tailscale/down` | admin | server/router/extensions.go:39 | pending | — |
| POST | `/api/extensions/tailscale/login` | admin | server/router/extensions.go:40 | pending | — |
| POST | `/api/extensions/tailscale/logout` | admin | server/router/extensions.go:41 | pending | — |
| POST | `/api/extensions/tailscale/start` | admin | server/router/extensions.go:42 | pending | — |
| POST | `/api/extensions/tailscale/stop` | admin | server/router/extensions.go:43 | pending | — |
| POST | `/api/extensions/tailscale/restart` | admin | server/router/extensions.go:44 | pending | — |
| POST | `/api/extensions/netbird/install` | admin | server/router/extensions.go:47 | pending | — |
| POST | `/api/extensions/netbird/uninstall` | admin | server/router/extensions.go:48 | pending | — |
| GET | `/api/extensions/netbird/status` | admin | server/router/extensions.go:49 | pending | — |
| POST | `/api/extensions/netbird/login` | admin | server/router/extensions.go:50 | pending | — |
| POST | `/api/extensions/netbird/down` | admin | server/router/extensions.go:51 | pending | — |
| POST | `/api/extensions/netbird/start` | admin | server/router/extensions.go:52 | pending | — |
| POST | `/api/extensions/netbird/stop` | admin | server/router/extensions.go:53 | pending | — |
| POST | `/api/extensions/netbird/restart` | admin | server/router/extensions.go:54 | pending | — |
| POST | `/api/hid/paste` | session + input owner | server/router/hid.go:23 | pending | — |
| GET | `/api/hid/shortcuts` | session | server/router/hid.go:25 | implemented-isolated | stage3-input.md; contract/filesystem |
| GET | `/api/hid/shortcut/leader-key` | session | server/router/hid.go:26 | implemented-isolated | stage3-input.md; contract/filesystem |
| GET | `/api/hid/mode` | session | server/router/hid.go:28 | implemented-isolated | stage3-input.md; contract/filesystem |
| GET | `/api/hid/leds` | session | server/router/hid.go:29 | pending | — |
| GET | `/api/hid/input-status` | session | server/router/hid.go:30 | implemented-isolated | stage3-input.md; contract/filesystem |
| POST | `/api/hid/mode` | admin | server/router/hid.go:36 | pending | — |
| POST | `/api/hid/reset` | admin | server/router/hid.go:37 | pending | — |
| POST | `/api/hid/shortcut` | admin | server/router/hid.go:38 | implemented-isolated | stage3-input.md; contract/filesystem |
| DELETE | `/api/hid/shortcut` | admin | server/router/hid.go:39 | implemented-isolated | stage3-input.md; contract/filesystem |
| POST | `/api/hid/shortcut/leader-key` | admin | server/router/hid.go:40 | implemented-isolated | stage3-input.md; contract/filesystem |
| POST | `/api/internal/usb/recover` | loopback-internal-token | server/router/hid.go:42 | pending | — |
| GET | `/api/mcp/config` | admin | server/router/mcp.go:31 | pending | — |
| POST | `/api/mcp/config` | admin | server/router/mcp.go:32 | pending | — |
| POST | `/api/mcp/key/regenerate` | admin | server/router/mcp.go:33 | pending | — |
| ANY | `/api/mcp` | mcp-api-key | server/router/mcp.go:37 | pending | — |
| POST | `/api/network/wifi` | public | server/router/network.go:14 | pending | — |
| POST | `/api/network/wifi/verify` | public | server/router/network.go:15 | pending | — |
| POST | `/api/network/wol` | session | server/router/network.go:19 | pending | — |
| GET | `/api/network/wol/mac` | session | server/router/network.go:20 | pending | — |
| GET | `/api/network/wol/interfaces` | session | server/router/network.go:21 | pending | — |
| DELETE | `/api/network/wol/mac` | admin | server/router/network.go:27 | pending | — |
| POST | `/api/network/wol/mac/name` | admin | server/router/network.go:28 | pending | — |
| GET | `/api/network/wifi/scan` | admin | server/router/network.go:30 | pending | — |
| POST | `/api/network/wifi/enabled` | admin | server/router/network.go:31 | pending | — |
| POST | `/api/network/wifi/band-preference` | admin | server/router/network.go:32 | pending | — |
| POST | `/api/network/wifi/configure` | admin | server/router/network.go:33 | pending | — |
| GET | `/api/network/wifi` | admin | server/router/network.go:34 | pending | — |
| POST | `/api/network/wifi/connect` | admin | server/router/network.go:35 | pending | — |
| POST | `/api/network/wifi/disconnect` | admin | server/router/network.go:36 | pending | — |
| GET | `/api/network/ipv6` | admin | server/router/network.go:37 | pending | — |
| POST | `/api/network/ipv6` | admin | server/router/network.go:38 | pending | — |
| GET | `/api/network/dns` | admin | server/router/network.go:39 | pending | — |
| POST | `/api/network/dns` | admin | server/router/network.go:40 | pending | — |
| GET | `/api/network/ethernet` | admin | server/router/network.go:41 | pending | — |
| POST | `/api/network/ethernet` | admin | server/router/network.go:42 | pending | — |
| GET | `/api/network/gateway` | admin | server/router/network.go:43 | pending | — |
| POST | `/api/network/gateway` | admin | server/router/network.go:44 | pending | — |
| GET | `/api/os/update` | admin | server/router/os_update.go:27 | pending | — |
| POST | `/api/os/update/apk/:action` | admin | server/router/os_update.go:31 | pending | — |
| GET | `/api/os/update/software` | admin | server/router/os_update.go:34 | pending | — |
| GET | `/api/os/update/software/status` | admin | server/router/os_update.go:38 | pending | — |
| GET | `/api/os/update/software/search` | admin | server/router/os_update.go:41 | pending | — |
| GET | `/api/os/update/software/updates` | admin | server/router/os_update.go:45 | pending | — |
| POST | `/api/os/update/software/remove/preview` | admin | server/router/os_update.go:49 | pending | — |
| POST | `/api/os/update/software/:action` | admin | server/router/os_update.go:60 | pending | — |
| GET | `/api/os/update/alpine` | admin | server/router/os_update.go:70 | pending | — |
| POST | `/api/os/update/alpine/build` | admin | server/router/os_update.go:73 | pending | — |
| POST | `/api/os/update/alpine/stage` | admin | server/router/os_update.go:103 | pending | — |
| POST | `/api/os/update/alpine/install` | admin | server/router/os_update.go:136 | pending | — |
| GET | `/api/picoclaw/screenshot` | loopback-internal-token | server/router/picoclaw.go:49 | pending | — |
| POST | `/api/picoclaw/actions` | loopback-internal-token | server/router/picoclaw.go:50 | pending | — |
| POST | `/api/picoclaw/mcp` | loopback-internal-token | server/router/picoclaw.go:51 | pending | — |
| POST | `/api/picoclaw/load-image` | loopback-internal-token | server/router/picoclaw.go:52 | pending | — |
| GET | `/api/picoclaw/runtime/session` | loopback-internal-token | server/router/picoclaw.go:53 | pending | — |
| POST | `/api/picoclaw/model/config` | admin | server/router/picoclaw.go:55 | pending | — |
| POST | `/api/picoclaw/agent/profile` | admin | server/router/picoclaw.go:56 | pending | — |
| GET | `/api/picoclaw/sessions` | admin | server/router/picoclaw.go:57 | pending | — |
| GET | `/api/picoclaw/sessions/:id` | admin | server/router/picoclaw.go:58 | pending | — |
| DELETE | `/api/picoclaw/sessions/:id` | admin | server/router/picoclaw.go:59 | pending | — |
| GET | `/api/picoclaw/runtime/status` | admin | server/router/picoclaw.go:60 | pending | — |
| DELETE | `/api/picoclaw/runtime/session` | admin | server/router/picoclaw.go:61 | pending | — |
| POST | `/api/picoclaw/runtime/install` | admin | server/router/picoclaw.go:62 | pending | — |
| POST | `/api/picoclaw/runtime/uninstall` | admin | server/router/picoclaw.go:63 | pending | — |
| POST | `/api/picoclaw/runtime/start` | admin | server/router/picoclaw.go:64 | pending | — |
| POST | `/api/picoclaw/runtime/stop` | admin | server/router/picoclaw.go:65 | pending | — |
| GET | `/api/picoclaw/gateway/ws` | admin | server/router/picoclaw.go:66 | pending | — |
| GET | `/api/addons/rustdesk/source` | admin | server/router/rustdesk.go:18 | pending | — |
| GET | `/api/addons/rustdesk/status` | admin | server/router/rustdesk.go:27 | pending | — |
| PUT | `/api/addons/rustdesk/config` | admin | server/router/rustdesk.go:37 | pending | — |
| POST | `/api/addons/rustdesk/:action` | admin | server/router/rustdesk.go:50 | pending | — |
| GET | `/api/storage/remote` | admin | server/router/storage.go:15 | pending | — |
| GET | `/api/storage/remote/connect` | admin | server/router/storage.go:16 | pending | — |
| POST | `/api/storage/remote/disconnect` | admin | server/router/storage.go:17 | pending | — |
| GET | `/api/storage/image` | admin | server/router/storage.go:18 | pending | — |
| GET | `/api/storage/image/mounted` | admin | server/router/storage.go:19 | pending | — |
| POST | `/api/storage/image/mount` | admin | server/router/storage.go:20 | pending | — |
| GET | `/api/storage/cdrom` | admin | server/router/storage.go:21 | pending | — |
| POST | `/api/storage/image/delete` | admin | server/router/storage.go:22 | pending | — |
| GET | `/api/stream/mjpeg` | session | server/router/stream.go:17 | pending | — |
| POST | `/api/stream/mjpeg/detect` | session | server/router/stream.go:18 | pending | — |
| POST | `/api/stream/mjpeg/detect/stop` | session | server/router/stream.go:19 | pending | — |
| GET | `/api/stream/state` | session | server/router/stream.go:21 | pending | — |
| POST | `/api/stream/state` | session | server/router/stream.go:22 | pending | — |
| GET | `/api/stream/audio` | session | server/router/stream.go:23 | pending | — |
| GET | `/api/stream/audio/status` | session | server/router/stream.go:24 | pending | — |
| GET | `/api/stream/video` | session | server/router/stream.go:26 | pending | — |
| GET | `/api/stream/video/direct` | session | server/router/stream.go:27 | pending | — |
| GET | `/api/stream/h264` | session | server/router/stream.go:28 | pending | — |
| GET | `/api/stream/h264/direct` | session | server/router/stream.go:29 | pending | — |
| GET | `/api/vm/date-time` | session | server/router/vm.go:20 | pending | — |
| POST | `/api/vm/date-time` | admin | server/router/vm.go:21 | pending | — |
| GET | `/api/vm/dashboard` | session | server/router/vm.go:23 | pending | — |
| GET | `/api/vm/logs` | admin | server/router/vm.go:24 | pending | — |
| GET | `/api/vm/logs/boots` | admin | server/router/vm.go:25 | pending | — |
| GET | `/api/vm/diagnostics` | admin | server/router/vm.go:26 | pending | — |
| GET | `/api/vm/diagnostics/report` | admin | server/router/vm.go:27 | pending | — |
| GET | `/api/vm/info` | session | server/router/vm.go:28 | pending | — |
| GET | `/api/vm/hardware` | session | server/router/vm.go:29 | pending | — |
| POST | `/api/vm/gpio` | session + input owner | server/router/vm.go:31 | pending | — |
| GET | `/api/vm/gpio` | session | server/router/vm.go:32 | pending | — |
| POST | `/api/vm/screen` | admin | server/router/vm.go:33 | pending | — |
| GET | `/api/vm/screen` | session | server/router/vm.go:34 | pending | — |
| GET | `/api/vm/input-region` | session | server/router/vm.go:36 | pending | — |
| POST | `/api/vm/input-region` | session | server/router/vm.go:37 | pending | — |
| GET | `/api/vm/input-resolution` | session | server/router/vm.go:38 | pending | — |
| GET | `/api/vm/terminal` | admin | server/router/vm.go:40 | pending | — |
| GET | `/api/vm/script` | admin | server/router/vm.go:42 | pending | — |
| POST | `/api/vm/script/upload` | admin | server/router/vm.go:43 | pending | — |
| POST | `/api/vm/script/run` | admin | server/router/vm.go:44 | pending | — |
| DELETE | `/api/vm/script` | admin | server/router/vm.go:45 | pending | — |
| GET | `/api/vm/device/virtual` | admin | server/router/vm.go:47 | pending | — |
| POST | `/api/vm/device/virtual` | admin | server/router/vm.go:48 | pending | — |
| PUT | `/api/vm/device/virtual` | admin | server/router/vm.go:49 | pending | — |
| GET | `/api/vm/memory/status` | admin | server/router/vm.go:51 | pending | — |
| POST | `/api/vm/memory/swap` | admin | server/router/vm.go:52 | pending | — |
| POST | `/api/vm/memory/video` | admin | server/router/vm.go:53 | pending | — |
| GET | `/api/vm/memory/limit` | admin | server/router/vm.go:55 | pending | — |
| POST | `/api/vm/memory/limit` | admin | server/router/vm.go:56 | pending | — |
| GET | `/api/vm/cpu-frequency` | admin | server/router/vm.go:58 | pending | — |
| POST | `/api/vm/cpu-frequency` | admin | server/router/vm.go:59 | pending | — |
| GET | `/api/vm/oled` | admin | server/router/vm.go:61 | pending | — |
| POST | `/api/vm/oled` | admin | server/router/vm.go:62 | pending | — |
| GET | `/api/vm/hdmi` | session | server/router/vm.go:65 | pending | — |
| POST | `/api/vm/hdmi/reset` | session | server/router/vm.go:66 | pending | — |
| POST | `/api/vm/hdmi/enable` | admin | server/router/vm.go:67 | pending | — |
| POST | `/api/vm/hdmi/disable` | admin | server/router/vm.go:68 | pending | — |
| POST | `/api/vm/hdmi/timeout` | admin | server/router/vm.go:69 | pending | — |
| GET | `/api/vm/ssh` | admin | server/router/vm.go:71 | pending | — |
| POST | `/api/vm/ssh/enable` | admin | server/router/vm.go:72 | pending | — |
| POST | `/api/vm/ssh/disable` | admin | server/router/vm.go:73 | pending | — |
| GET | `/api/vm/swap` | admin | server/router/vm.go:75 | pending | — |
| POST | `/api/vm/swap` | admin | server/router/vm.go:76 | pending | — |
| GET | `/api/vm/mouse-jiggler` | admin | server/router/vm.go:78 | pending | — |
| POST | `/api/vm/mouse-jiggler/` | admin | server/router/vm.go:79 | pending | — |
| GET | `/api/vm/hostname` | session | server/router/vm.go:81 | pending | — |
| POST | `/api/vm/hostname` | admin | server/router/vm.go:82 | pending | — |
| GET | `/api/vm/web-title` | session | server/router/vm.go:84 | implemented-isolated | validation.md; API/contract/UI slice |
| POST | `/api/vm/web-title` | admin | server/router/vm.go:85 | implemented-isolated | validation.md; API/contract/UI slice |
| GET | `/api/vm/mdns` | admin | server/router/vm.go:87 | pending | — |
| POST | `/api/vm/mdns/enable` | admin | server/router/vm.go:88 | pending | — |
| POST | `/api/vm/mdns/disable` | admin | server/router/vm.go:89 | pending | — |
| POST | `/api/vm/tls` | admin | server/router/vm.go:91 | pending | — |
| GET | `/api/vm/autostart` | admin | server/router/vm.go:93 | pending | — |
| GET | `/api/vm/autostart/:name` | admin | server/router/vm.go:94 | pending | — |
| DELETE | `/api/vm/autostart/:name` | admin | server/router/vm.go:95 | pending | — |
| POST | `/api/vm/autostart/:name` | admin | server/router/vm.go:96 | pending | — |
| POST | `/api/vm/system/reboot` | admin | server/router/vm.go:98 | pending | — |
| GET | `/api/ws` | session | server/router/ws.go:14 | partial-isolated | stage3-ws.md; real sockets/HID fixtures; snapshots/addon arbitration pending |
