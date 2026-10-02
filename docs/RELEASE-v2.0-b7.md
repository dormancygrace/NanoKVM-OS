# NanoKVM OS v2.0-b7

### 🔐 Access and input control

- Require changing the factory web password before accessing device controls. The password-change page also works on small screens; cancelling signs out.
- Enable login lockout for new configurations: **5 failed attempts / 5 minutes**. Existing configured values are preserved; IPv6 attempts are grouped by /64.
- Bind paste and ATX actions to the **browser tab that currently owns input control**, including when several tabs share the same login. Show a clear message when another tab owns control.
- Make paste respect an active PicoClaw session. Keep HDMI disabled when a viewer requests a capture reset, and restrict monitor/EDID profile changes to administrators.
- Bound ordinary API requests, script uploads and input WebSocket messages. Validate bitrate and GOP settings before passing them to the native runtime.
- Reject OpenVPN profile lines that cannot be parsed consistently by the importer and OpenVPN.

### 💾 Settings reliability

- Save server settings, shortcuts and Wake-on-LAN history atomically.
- Preserve unknown YAML keys and unrelated settings when changing HTTPS configuration.
- Improve handling of incomplete generated TLS certificates and damaged configuration files.
- Validate hostnames and update complete host entries without altering unrelated names such as localhost.
- Serialize Wake-on-LAN history updates and limit the list to 100 entries, preferring to remove unnamed entries.

### 🖥️ Interface fixes

- Deliver the HTTPS-change confirmation before restarting the server, and reconnect after a restart interrupts the request.
- Clear the input-control indicator when the WebSocket disconnects.
- Report logout and autostart-save errors instead of failing silently.
- Correct PicoClaw history pagination and encode session IDs and autostart names in API URLs.
- Send mouse-jiggler settings to the exact server route.

### 📦 Native package updates

- Remove the obsolete **.nkos updater**, its API endpoints and background release checks. NanoKVM OS v2 continues to use signed native APK packages.
- Retain the detached APK transaction worker and automatic service handling through OpenRC.
- Update installation instructions and correct the Wi-Fi runtime test for DHCP logging.

### 🚀 Installation and updates

For a fresh installation, extract **NanoKVM-OS-v2.0-b7.img.zip** and write the `.img` to an SD card of at least **2 GB**.

From **v2.0-a2 or later**, use **Settings → System → Updates** (on a2: **Settings → Updates → Package updates**), or run as root:

```sh
apk update
apk upgrade
reboot
```

If NanoKVM packages were installed from local `.apk` files, first release their package pins:

```sh
apk update
apk add nanokvm-base nanokvm-app nanokvm-release
apk upgrade
reboot
```

Settings, user data and independently installed packages are retained. Linux remains **7.2.6-nanokvm-os-r1**; the kernel and native capture libraries are unchanged from b6.

### 📝 Notes

Devices still using the factory web password must change it after updating. For the administrator account, this also changes the Linux root password used by SSH.
