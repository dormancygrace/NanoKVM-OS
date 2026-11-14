# Computer sensors

The NanoKVM can show the sensors of the computer it is plugged into: temperatures, voltages, fan speeds, power, and whatever else that computer measures. A program on the computer, the **agent** (`nanokvm-sensors`), reads them and sends a report every few seconds over the USB cable. The NanoKVM keeps the last report and shows it in **Settings > Services > Computer sensors**.

This is an add-on, `nanokvm-host-sensors` (Settings > Software > Add-ons). Without it nothing of this runs or shows: the page is not in the menu, the serial port stays free and the report address refuses. The add-on carries the agent; the receiving side is part of NanoKVM OS.

Agents: Windows (64-bit, 10 and 11) and Linux (any with `/sys/class/hwmon`; a systemd service).

## Setting up

1. Install the add-on. **Services > Computer sensors** appears.
2. Choose how the readings arrive:

   | Mode | The NanoKVM needs | Notes |
   | --- | --- | --- |
   | USB serial | USB serial in the USB composition | The NanoKVM holds `/dev/ttyGS0` while the mode is set: the web terminal and the console test cannot use that port. |
   | USB network | the USB network in the USB composition | Reports are taken from the USB network only, never from the LAN, Wi-Fi or a VPN. |

   Serial, the USB network and all three HID functions do not fit into the USB endpoint budget together ([USB composition](usb-composition.md)); one of the two is enough.
3. On the computer, open the **NANOKVM** helper disk (below) and double-click `NanoKVM-Sensors-Setup-Windows.exe`. Its window shows the version it brings, the installed agent's and the driver's, and offers what fits: **Install**, **Update** when the installed agent is older, **Remove**. It asks Windows for administrator rights.

   Installing copies the agent to `Program Files\NanoKVM`, installs the PawnIO driver when it is missing (a checkbox), registers a task that starts the agent as SYSTEM with Windows, and starts it. The installed agent writes its state changes to `%ProgramData%\NanoKVM\nanokvm-sensors.log`.

   The same from an administrator's console: `NanoKVM-Sensors-Setup-Windows.exe install` (with the agent's options, which become the installed agent's) and `uninstall`, which stops and removes the task and the copy; the driver stays.

The page names the version of the agent that reports, and says when the add-on brings a newer one: run the setup from the helper disk again to update.

## The helper disk

The agent comes to the computer the way the [serial console helper](usb-composition.md#linux-host-setup-the-console-helper) does: on the small read-only disk labelled `NANOKVM` that the computer sees next to the virtual drive (a second LUN of it, so it costs no USB endpoint). It is one disk for both, with names that say what each file sets up: `NanoKVM-Serial-Console-Setup-Linux.sh`, `NanoKVM-Sensors-Setup-Windows.exe`, `NanoKVM-Sensors-Setup-Linux.sh`, and a `README.txt`.

The image with the console helper alone is made when NanoKVM OS is built. The agent belongs to the add-on, so with the add-on installed the server puts the disk together itself (`server/helperdisk`, a 12 MiB FAT12 image in `/var/lib/nanokvm/helper-disk.img`, rebuilt when the add-on or the console helper changes and removed with the add-on), and `S03usbdev` attaches that one instead. The disk is shown while the sensors are on or USB serial is in the composition; it needs the virtual disk in the USB composition. The computer keeps the disk it has until the USB devices reconnect: after installing the add-on or turning the sensors on, apply the USB composition again or reconnect the cable. The page says which it is: the computer has the disk, the agent appears on it after a reconnect, or there is no disk. Only then does it offer the download (`https://<nanokvm>/nanokvm-sensors.exe`) instead.

## The agent

The agent finds the NanoKVM by itself: the serial port by the USB IDs, the USB network by the adapter's address prefix (`48:da:35:6d`). Each report goes to the serial port when the NanoKVM has one and over the USB network otherwise, so the agent needs no setting when the mode changes. `--help` lists the options (`--serial COMn`, `--network URL`, `--usb-id VID:PID` for a changed USB identity, `--interval SECONDS`), `run` is the agent itself (what the task starts), `--once` prints one report.

## Linux

`NanoKVM-Sensors-Setup-Linux.sh` on the helper disk is the agent itself: a POSIX shell script with no dependency beyond the usual tools (`stty`, `dd`, `ip`, and `curl` or `wget` for the USB network). As root, with the disk mounted:

```
sh /mnt/NanoKVM-Sensors-Setup-Linux.sh            # in a terminal: asks to install, update or remove
sh /mnt/NanoKVM-Sensors-Setup-Linux.sh install    # the same without asking; options become the agent's
sh /mnt/NanoKVM-Sensors-Setup-Linux.sh uninstall
sh /mnt/NanoKVM-Sensors-Setup-Linux.sh status
```

`install` copies the script to `/usr/local/sbin/nanokvm-sensors` and starts it as the systemd service `nanokvm-sensors`. Without systemd it says what to start by hand (`run`).

It reads what the kernel offers in `/sys/class/hwmon`: temperatures, fans, voltages, currents, power, fan control. Drives are named by their model and the processor by its name; the processor's load and the memory in use come from `/proc`. Board sensors (fans, voltages) show only when the board's sensor driver is loaded (`it87`, `nct6775` and the like); the agent loads no module.

- **USB serial:** the NanoKVM's `ttyACM` port, found by the USB IDs. A port that is the computer's login console (the [serial console helper](usb-composition.md#linux-host-setup-the-console-helper) sets one up) is never used: the reports would be typed into its prompt. One port carries one thing, so a computer with the console on that port sends its sensors over the USB network.
- **USB network:** the NanoKVM's adapter, found by its address prefix. A server usually configures no new adapter by itself; the agent then gives the adapter an address of the NanoKVM's own network, `10.X.Y.2/24`, where X and Y are the last two bytes of the adapter's address (the NanoKVM derives its network from the same bytes). That is an address on the NanoKVM's link and nothing else: no route, no DNS, gone with the adapter. A NanoKVM whose USB network prefix was changed needs DHCP on the adapter, or `--network URL`.

## The driver

The agent reads sensors with [LibreHardwareMonitorLib](https://github.com/LibreHardwareMonitor/LibreHardwareMonitor). Graphics cards, memory and drives are read through their vendors' and Windows' interfaces. Processor and board sensors (temperatures, fans, voltages) need registers that only a kernel driver reaches: [PawnIO](https://pawnio.eu), signed, which the library uses and does not install. The agent carries PawnIO's installer, as LibreHardwareMonitor does, and `install` asks before running it (`--yes`: without asking, `--no-driver`: not at all).

Without the driver, or without administrator rights, the library returns zeros and defaults for those parts. The agent then leaves them out, except the load, and marks the report; the page says that processor and board sensors may be missing.

## What the NanoKVM accepts

The computer is not trusted. A report is one JSON object, a line on the serial port or the body of `POST /api/host-sensors`:

```json
{"v":1,"host":"PC","os":"Windows 11 Pro 26200","agent":"nanokvm-sensors/1 windows","interval":5,
 "sensors":[{"hw":"AMD Ryzen 7 9700X","type":"cpu","name":"Core (Tctl/Tdie)","kind":"temperature","value":51.5}]}
```

- At most 256 KiB and 1024 readings; texts at most 96 printable characters. Anything else rejects the report.
- `kind` names the quantity and with it the unit (`temperature` °C, `voltage` V, `fan` RPM, `power` W, and the others in `server/hostsensors`). A reading of an unknown kind is dropped, so a newer agent works with an older NanoKVM.
- On the serial port the NanoKVM writes the line `nanokvm-ready` every second while it reads reports. The Linux agent writes a report only after hearing it; the Windows agent reads the port's modem lines instead (the NanoKVM's side raises DSR and carrier while it has the port open). Neither writes to a port nobody reads: a write that is started and cut, repeated for minutes, stops the NanoKVM's USB controller from taking data until the USB devices reconnect.
- Only the last report is kept, in memory. After three of the agent's intervals without a report (at least 15 s) the page says the data is not fresh.

`POST /api/host-sensors` needs no login, because the computer has none. It answers only in USB network mode and only to a peer on the USB network, with a JSON content type (so a web page in the computer's browser cannot post without a preflight). The NanoKVM's certificate is its own; for the address found on the USB adapter the agent does not check it: the peer is the device at the other end of the cable and the report holds nothing secret. A URL given with `--network` is checked as usual.

## Building

`scripts/build-addon.py --addon nanokvm-host-sensors --dotnet <dotnet>` builds the package: the agent (`addons/host-sensors/windows`, .NET Framework 4.7.2, one exe with its libraries inside) with the **latest** release of PawnIO's installer ([namazso/PawnIO.Setup](https://github.com/namazso/PawnIO.Setup)), whose version and SHA-256 are recorded in the package's `source.json`. `--pawnio FILE` uses a local copy instead.
