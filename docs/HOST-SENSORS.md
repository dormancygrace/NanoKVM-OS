# Computer sensors

The NanoKVM can show the sensors of the computer it is plugged into: temperatures, voltages, fan speeds, power, and whatever else that computer measures. A program on the computer, the **agent** (`nanokvm-sensors`), reads them and sends a report every few seconds over the USB cable. The NanoKVM keeps the last report and shows it in **Settings > Services > Computer sensors**.

This is an add-on, `nanokvm-host-sensors` (Settings > Software > Add-ons). Without it nothing of this runs or shows: the page is not in the menu, the serial port stays free and the report address refuses. The add-on carries the agent; the receiving side is part of NanoKVM OS.

Agents: Windows (64-bit, 10 and 11). Linux is not there yet.

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

The agent comes to the computer the way the [serial console helper](usb-composition.md#linux-host-setup-the-console-helper) does: on the small read-only disk labelled `NANOKVM` that the computer sees next to the virtual drive (a second LUN of it, so it costs no USB endpoint). It is one disk for both, with names that say what each file sets up: `NanoKVM-Serial-Console-Setup-Linux.sh`, `NanoKVM-Sensors-Setup-Windows.exe`, and a `README.txt`.

The image with the console helper alone is made when NanoKVM OS is built. The agent belongs to the add-on, so with the add-on installed the server puts the disk together itself (`server/helperdisk`, a 12 MiB FAT12 image in `/var/lib/nanokvm/helper-disk.img`, rebuilt when the add-on or the console helper changes and removed with the add-on), and `S03usbdev` attaches that one instead. The disk is shown while the sensors are on or USB serial is in the composition; it needs the virtual disk in the USB composition. The computer keeps the disk it has until the USB devices reconnect: after installing the add-on or turning the sensors on, apply the USB composition again or reconnect the cable. The page says which it is: the computer has the disk, the agent appears on it after a reconnect, or there is no disk. Only then does it offer the download (`https://<nanokvm>/nanokvm-sensors.exe`) instead.

## The agent

The agent finds the NanoKVM by itself: the serial port by the USB IDs, the USB network by the adapter's address prefix (`48:da:35:6d`). Each report goes to the serial port when the NanoKVM has one and over the USB network otherwise, so the agent needs no setting when the mode changes. `--help` lists the options (`--serial COMn`, `--network URL`, `--usb-id VID:PID` for a changed USB identity, `--interval SECONDS`), `run` is the agent itself (what the task starts), `--once` prints one report.

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
- Only the last report is kept, in memory. After three of the agent's intervals without a report (at least 15 s) the page says the data is not fresh.

`POST /api/host-sensors` needs no login, because the computer has none. It answers only in USB network mode and only to a peer on the USB network, with a JSON content type (so a web page in the computer's browser cannot post without a preflight). The NanoKVM's certificate is its own; for the address found on the USB adapter the agent does not check it: the peer is the device at the other end of the cable and the report holds nothing secret. A URL given with `--network` is checked as usual.

## Building

`scripts/build-addon.py --addon nanokvm-host-sensors --dotnet <dotnet>` builds the package: the agent (`addons/host-sensors/windows`, .NET Framework 4.7.2, one exe with its libraries inside) with the **latest** release of PawnIO's installer ([namazso/PawnIO.Setup](https://github.com/namazso/PawnIO.Setup)), whose version and SHA-256 are recorded in the package's `source.json`. `--pawnio FILE` uses a local copy instead.
