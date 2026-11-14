// nanokvm-sensors for Windows: reads this computer's sensors (temperatures,
// voltages, fan speeds and the like) with LibreHardwareMonitorLib and sends
// a report every few seconds to the NanoKVM plugged into it, over the
// NanoKVM's USB serial port or its USB network. The NanoKVM's dashboard
// shows the last report. The report format is in server/hostsensors.
//
// Most processor and board sensors need administrator rights and the PawnIO
// driver (https://pawnio.eu), which this program neither carries nor
// installs; without them it sends what it can read and says so ("limited").
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Management;
using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Security.Principal;
using System.Text;
using System.Threading;
using LibreHardwareMonitor.Hardware;
using Microsoft.Win32;
using Microsoft.Win32.SafeHandles;

namespace NanoKvmSensors
{
    internal static class Program
    {
        private const string AgentVersion = "1";
        private const string TaskName = "NanoKVM Sensors";
        // The report format's bounds (server/hostsensors).
        private const int MaxSensors = 1024;
        private const int MaxText = 96;

        private static int Main(string[] args)
        {
            // The libraries are inside the exe: one file to copy.
            AppDomain.CurrentDomain.AssemblyResolve += LoadEmbedded;
            try
            {
                return Run(args);
            }
            catch (Exception e)
            {
                Console.Error.WriteLine("nanokvm-sensors: " + e.Message);
                return 1;
            }
        }

        private static Assembly LoadEmbedded(object sender, ResolveEventArgs e)
        {
            string name = "lib/" + new AssemblyName(e.Name).Name + ".dll";
            using (Stream s = Assembly.GetExecutingAssembly().GetManifestResourceStream(name))
            {
                if (s == null) return null;
                byte[] data = new byte[s.Length];
                int done = 0;
                while (done < data.Length) done += s.Read(data, done, data.Length - done);
                return Assembly.Load(data);
            }
        }

        private sealed class Options
        {
            public string Transport = "auto"; // auto, serial, network
            public string Port;               // COMn, or found by the USB IDs
            public string Url;                // https://address, or found by the adapter
            public string UsbId = "3346:1009";
            public int Interval = 5;
            public bool Once;
            public string LogFile;            // state changes go here too
            public bool Yes;      // install: do not ask before installing the driver
            public bool NoDriver; // install: leave the driver alone
        }

        private const string Usage =
@"nanokvm-sensors: send this computer's sensors to the NanoKVM plugged into it.

  nanokvm-sensors [options]     run until stopped (Ctrl+C)
  nanokvm-sensors --once        print one report and exit
  nanokvm-sensors install       copy this program to Program Files\NanoKVM
                                and start it with Windows (a scheduled task
                                as SYSTEM; needs an administrator's console),
                                from wherever it is run, such as the NanoKVM's
                                helper disk (sensors.exe there). Offers to
                                install the PawnIO driver first when it is
                                missing or older than 2.0 (--yes: without
                                asking, --no-driver: not at all)
  nanokvm-sensors uninstall     stop and remove that task (the driver stays)

Options:
  --serial [COMn]     USB serial only; the port is found by the USB IDs
  --network [URL]     USB network only; URL such as https://10.1.2.1
  --usb-id VID:PID    the NanoKVM's USB IDs when changed (default 3346:1009)
  --interval SECONDS  period, 1 to 3600 (default 5)
  --log FILE          also write what happens (state changes only) to FILE;
                      the installed task logs to
                      %ProgramData%\NanoKVM\nanokvm-sensors.log

Without --serial or --network each report goes to the serial port when the
NanoKVM has one, and to the USB network otherwise. The NanoKVM takes one of
the two: Dashboard > Computer sensors.

Processor and board sensors need administrator rights and the PawnIO driver
(https://pawnio.eu, GPL-2.0; its installer is inside this program). Without
them the report is marked as limited.";

        private static int Run(string[] args)
        {
            var o = new Options();
            string command = null;
            for (int i = 0; i < args.Length; i++)
            {
                string a = args[i];
                Func<bool> hasValue = () => i + 1 < args.Length && !args[i + 1].StartsWith("-");
                switch (a)
                {
                    case "install": case "uninstall": command = a; break;
                    case "--once": o.Once = true; break;
                    case "--yes": o.Yes = true; break;
                    case "--no-driver": o.NoDriver = true; break;
                    case "--serial": o.Transport = "serial"; if (hasValue()) o.Port = args[++i]; break;
                    case "--network": o.Transport = "network"; if (hasValue()) o.Url = args[++i]; break;
                    case "--log":
                        if (!hasValue()) throw new ArgumentException("--log needs a file");
                        o.LogFile = args[++i];
                        break;
                    case "--usb-id":
                        if (!hasValue()) throw new ArgumentException("--usb-id needs VID:PID");
                        o.UsbId = args[++i];
                        break;
                    case "--interval":
                        if (!hasValue() || !int.TryParse(args[++i], out o.Interval) || o.Interval < 1 || o.Interval > 3600)
                            throw new ArgumentException("--interval needs 1 to 3600 seconds");
                        break;
                    case "-h": case "--help": case "/?":
                        Console.WriteLine(Usage);
                        return 0;
                    default:
                        throw new ArgumentException("unknown argument " + a + " (see --help)");
                }
            }
            string[] id = o.UsbId.Split(':');
            if (id.Length != 2 || !IsHex4(id[0]) || !IsHex4(id[1])) throw new ArgumentException("--usb-id needs VID:PID, four hex digits each");
            if (o.Url != null)
            {
                Uri u;
                if (!Uri.TryCreate(o.Url, UriKind.Absolute, out u) || (u.Scheme != "https" && u.Scheme != "http"))
                    throw new ArgumentException("--network needs a URL such as https://10.1.2.1");
            }
            if (command == "install") return Install(args, o);
            if (command == "uninstall")
            {
                // Deleting a task leaves its running program: end it first.
                Schtasks("/End /TN \"" + TaskName + "\"", true);
                int code = Schtasks("/Delete /F /TN \"" + TaskName + "\"");
                // The copy install made, unless this is that copy running.
                string copy = InstalledCopy();
                if (!string.Equals(Path.GetFullPath(Process.GetCurrentProcess().MainModule.FileName), copy, StringComparison.OrdinalIgnoreCase))
                {
                    for (int attempt = 0; attempt < 10 && File.Exists(copy); attempt++)
                    {
                        try { File.Delete(copy); } catch (Exception) { Thread.Sleep(500); }
                    }
                }
                return code;
            }

            if (o.LogFile != null)
            {
                logFile = Path.GetFullPath(o.LogFile);
                try { Directory.CreateDirectory(Path.GetDirectoryName(logFile)); } catch (Exception) { }
            }
            bool limited = !IsElevated() || !PawnIoInstalled();
            bool driver = !limited;
            var computer = new Computer
            {
                IsCpuEnabled = true, IsGpuEnabled = true, IsMemoryEnabled = true, IsMotherboardEnabled = true,
                IsStorageEnabled = true, IsControllerEnabled = true, IsPsuEnabled = true, IsBatteryEnabled = true,
                IsNetworkEnabled = false
            };
            computer.Open();
            try
            {
                if (o.Once)
                {
                    Console.WriteLine(Report(computer, o.Interval, limited, driver));
                    return 0;
                }
                var stop = new ManualResetEvent(false);
                Console.CancelKeyPress += (s, e) => { e.Cancel = true; stop.Set(); };
                Log("started" + (limited ? " (limited: no administrator rights or no PawnIO driver)" : ""));
                var sender = new Sender(o);
                do
                {
                    sender.Send(Report(computer, o.Interval, limited, driver));
                } while (!stop.WaitOne(TimeSpan.FromSeconds(o.Interval)));
                sender.Close();
                return 0;
            }
            finally
            {
                computer.Close();
            }
        }

        private static bool IsHex4(string s)
        {
            return s.Length == 4 && s.All(Uri.IsHexDigit);
        }

        private static string logFile;

        // Log: state changes only, so the file stays small; it starts anew
        // once it has grown past 256 KiB.
        private static void Log(string text)
        {
            Console.Error.WriteLine(DateTime.Now.ToString("HH:mm:ss", CultureInfo.InvariantCulture) + " " + text);
            if (logFile == null) return;
            try
            {
                var info = new FileInfo(logFile);
                if (info.Exists && info.Length > 256 << 10) info.Delete();
                File.AppendAllText(logFile, DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss", CultureInfo.InvariantCulture) + " " + text + "\r\n");
            }
            catch (Exception)
            {
                // A log that cannot be written must not stop the agent.
            }
        }

        private static bool IsElevated()
        {
            using (WindowsIdentity identity = WindowsIdentity.GetCurrent())
                return new WindowsPrincipal(identity).IsInRole(WindowsBuiltInRole.Administrator);
        }

        // What LibreHardwareMonitorLib itself looks for: the installer's
        // entry in the registry. null: not installed.
        private static Version PawnIoVersion()
        {
            foreach (RegistryView view in new[] { RegistryView.Registry64, RegistryView.Registry32 })
            {
                using (RegistryKey root = RegistryKey.OpenBaseKey(RegistryHive.LocalMachine, view))
                using (RegistryKey key = root.OpenSubKey(@"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\PawnIO"))
                {
                    if (key == null) continue;
                    Version version;
                    return Version.TryParse(Convert.ToString(key.GetValue("DisplayVersion")), out version) ? version : new Version(0, 0);
                }
            }
            return null;
        }

        private static bool PawnIoInstalled()
        {
            return PawnIoVersion() != null;
        }

        // ---- the report -------------------------------------------------

        private static string Kind(SensorType type, ref float value)
        {
            switch (type.ToString())
            {
                case "Temperature": return "temperature";
                case "Voltage": return "voltage";
                case "Current": return "current";
                case "Power": return "power";
                case "Fan": return "fan";
                case "Control": return "control";
                case "Load": return "load";
                case "Level": return "level";
                case "Clock": return "clock";
                case "Frequency": return "frequency";
                case "Data": return "data";
                case "SmallData": return "smalldata";
                case "Throughput": return "throughput";
                case "Energy": return "energy";
                case "Humidity": return "humidity";
                case "Flow": return "flow";
                case "Noise": return "noise";
                case "Factor": return "factor";
                default: return null; // a kind the report format does not have
            }
        }

        private static string HardwareClass(HardwareType type)
        {
            string name = type.ToString();
            if (name == "Cpu") return "cpu";
            if (name.StartsWith("Gpu")) return "gpu";
            if (name == "Motherboard" || name == "SuperIO" || name == "EmbeddedController") return "motherboard";
            if (name == "Memory") return "memory";
            if (name == "Storage") return "storage";
            if (name == "Network") return "network";
            if (name == "Cooler") return "cooler";
            if (name == "Psu") return "psu";
            if (name == "Battery") return "battery";
            return "other";
        }

        // Without the PawnIO driver, or without the rights to open it, the
        // library reads no processor or board register and reports zeros and
        // defaults for them (0 W, 0 degrees, 1.55 V): of those parts only the
        // load, which Windows itself counts, is real. driver: it can be used.
        private static void Collect(IHardware hardware, List<string> sensors, bool driver)
        {
            hardware.Update();
            string hw = Json(hardware.Name), type = HardwareClass(hardware.HardwareType);
            bool loadOnly = !driver && (type == "cpu" || type == "motherboard");
            foreach (ISensor sensor in hardware.Sensors)
            {
                if (sensors.Count >= MaxSensors) return;
                if (!sensor.Value.HasValue) continue;
                float value = sensor.Value.Value;
                if (float.IsNaN(value) || float.IsInfinity(value)) continue;
                string kind = Kind(sensor.SensorType, ref value);
                string name = Json(sensor.Name);
                if (kind == null || name == "\"\"") continue;
                if (loadOnly && kind != "load") continue;
                sensors.Add("{\"hw\":" + hw + ",\"type\":\"" + type + "\",\"name\":" + name + ",\"kind\":\"" + kind +
                            "\",\"value\":" + value.ToString("0.####", CultureInfo.InvariantCulture) + "}");
            }
            foreach (IHardware sub in hardware.SubHardware) Collect(sub, sensors, driver);
        }

        private static string Report(Computer computer, int interval, bool limited, bool driver)
        {
            var sensors = new List<string>();
            foreach (IHardware hardware in computer.Hardware) Collect(hardware, sensors, driver);
            var sb = new StringBuilder(64 * sensors.Count + 256);
            sb.Append("{\"v\":1,\"host\":").Append(Json(Environment.MachineName))
              .Append(",\"os\":").Append(Json(OsName()))
              .Append(",\"agent\":").Append(Json("nanokvm-sensors/" + AgentVersion + " windows"))
              .Append(",\"interval\":").Append(interval.ToString(CultureInfo.InvariantCulture));
            if (limited) sb.Append(",\"limited\":true");
            sb.Append(",\"sensors\":[").Append(string.Join(",", sensors)).Append("]}");
            return sb.ToString();
        }

        private static string osName;

        private static string OsName()
        {
            if (osName != null) return osName;
            try
            {
                using (RegistryKey key = Registry.LocalMachine.OpenSubKey(@"SOFTWARE\Microsoft\Windows NT\CurrentVersion"))
                {
                    string product = Convert.ToString(key.GetValue("ProductName"));
                    string build = Convert.ToString(key.GetValue("CurrentBuildNumber"));
                    int number;
                    // The registry still says "Windows 10" on Windows 11.
                    if (int.TryParse(build, out number) && number >= 22000) product = product.Replace("Windows 10", "Windows 11");
                    osName = (product + " " + build).Trim();
                }
            }
            catch (Exception)
            {
                osName = Environment.OSVersion.VersionString;
            }
            return osName;
        }

        // Json writes a text as a JSON string the NanoKVM accepts: printable
        // characters only, at most MaxText of them.
        private static string Json(string text)
        {
            var sb = new StringBuilder("\"");
            int count = 0;
            foreach (char c in (text ?? "").Trim())
            {
                if (count >= MaxText) break;
                if (char.IsControl(c) || char.IsSurrogate(c) || c == (char)0x2028 || c == (char)0x2029) continue;
                if (c == '"' || c == '\\') sb.Append('\\').Append(c);
                else if (c < 0x7f) sb.Append(c);
                else sb.Append("\\u").Append(((int)c).ToString("x4", CultureInfo.InvariantCulture));
                count++;
            }
            return sb.Append('"').ToString();
        }

        // ---- the transports ---------------------------------------------

        private sealed class Sender
        {
            private readonly Options o;
            private SerialLink port;
            private string state = "";

            public Sender(Options o)
            {
                this.o = o;
                // The NanoKVM's own certificate is self-signed; see Post.
                ServicePointManager.SecurityProtocol |= (SecurityProtocolType)3072 | (SecurityProtocolType)12288;
                ServicePointManager.Expect100Continue = false;
            }

            private void State(string text)
            {
                if (text == state) return;
                state = text;
                Log(text);
            }

            public void Send(string report)
            {
                string serialError = null;
                if (o.Transport != "network")
                {
                    serialError = Serial(report);
                    if (serialError == null) return;
                    if (o.Transport == "serial") { State(serialError); return; }
                }
                string networkError = Post(report);
                if (networkError == null) return;
                State(o.Transport == "network" ? networkError : serialError + "; " + networkError);
            }

            public void Close()
            {
                if (port != null) { port.Dispose(); port = null; }
            }

            private string Serial(string report)
            {
                try
                {
                    if (port == null)
                    {
                        string name = o.Port ?? FindPort(o.UsbId);
                        if (name == null) return "no NanoKVM serial port (USB serial is not in its USB composition)";
                        port = new SerialLink(name);
                    }
                    // Nothing is written while nobody reads the port there:
                    // the NanoKVM's side raises the modem lines when it opens
                    // the port (Computer sensors set to USB serial).
                    if (!port.Ready())
                        return port.Name + ": the NanoKVM does not have the port open (Computer sensors is not set to USB serial)";
                    // A line of its own, whatever a write that was cut short
                    // left on the wire: the NanoKVM skips empty lines.
                    if (!port.Write(Encoding.ASCII.GetBytes("\n" + report + "\n"), 3000))
                        return port.Name + ": the NanoKVM did not take the report in time";
                    State("sending to " + port.Name);
                    return null;
                }
                catch (Exception e)
                {
                    // The port goes with every change of the NanoKVM's USB
                    // composition; it is found again at the next report.
                    string name = port != null ? port.Name : (o.Port ?? "serial port");
                    Close();
                    return name + ": " + e.Message;
                }
            }

            // FindPort: the COM port of the USB device with the NanoKVM's IDs.
            private static string FindPort(string usbId)
            {
                string[] id = usbId.ToUpperInvariant().Split(':');
                string pattern = "USB\\\\VID_" + id[0] + "&PID_" + id[1] + "%";
                using (var search = new ManagementObjectSearcher(
                    "SELECT Name FROM Win32_PnPEntity WHERE PNPDeviceID LIKE '" + pattern + "' AND Name LIKE '%(COM%'"))
                {
                    foreach (ManagementBaseObject device in search.Get())
                    {
                        string name = Convert.ToString(device["Name"]);
                        int open = name.LastIndexOf("(COM", StringComparison.Ordinal), close = name.LastIndexOf(')');
                        if (open >= 0 && close > open) return name.Substring(open + 1, close - open - 1);
                    }
                }
                return null;
            }

            // The NanoKVM's address on its USB network: the adapter is known
            // by the first four bytes of its address (S03usbdev sets
            // 48:da:35:6d on the computer's side), the NanoKVM is its DHCP
            // server, or .1 of the adapter's network.
            private static string FindAddress()
            {
                foreach (NetworkInterface nic in NetworkInterface.GetAllNetworkInterfaces())
                {
                    byte[] mac = nic.GetPhysicalAddress().GetAddressBytes();
                    if (mac.Length != 6 || mac[0] != 0x48 || mac[1] != 0xda || mac[2] != 0x35 || mac[3] != 0x6d) continue;
                    if (nic.OperationalStatus != OperationalStatus.Up) continue;
                    IPInterfaceProperties ip = nic.GetIPProperties();
                    foreach (IPAddress dhcp in ip.DhcpServerAddresses)
                        if (dhcp.AddressFamily == AddressFamily.InterNetwork) return dhcp.ToString();
                    foreach (UnicastIPAddressInformation own in ip.UnicastAddresses)
                    {
                        if (own.Address.AddressFamily != AddressFamily.InterNetwork) continue;
                        byte[] b = own.Address.GetAddressBytes();
                        if (b[0] == 169 && b[1] == 254) continue; // no DHCP answer yet
                        b[3] = 1;
                        return new IPAddress(b).ToString();
                    }
                }
                return null;
            }

            private string Post(string report)
            {
                string url = o.Url;
                bool found = url == null;
                if (found)
                {
                    string address = FindAddress();
                    if (address == null) return "no NanoKVM USB network (it is not in its USB composition, or has no address yet)";
                    url = "https://" + address;
                }
                try
                {
                    var request = (HttpWebRequest)WebRequest.Create(url.TrimEnd('/') + "/api/host-sensors");
                    request.Method = "POST";
                    request.ContentType = "application/json";
                    request.Timeout = 5000;
                    request.ReadWriteTimeout = 5000;
                    request.Proxy = null; // the USB link is direct; a system proxy must not see the report
                    request.AllowAutoRedirect = false;
                    // The NanoKVM presents a certificate it made itself, for
                    // names this address is not among. On the USB link the
                    // peer is the device at the other end of the cable, and
                    // the report holds nothing secret, so for an address
                    // found on that adapter the certificate is not checked.
                    // A URL given with --network is checked as usual.
                    if (found) request.ServerCertificateValidationCallback = (s, c, ch, e) => true;
                    byte[] body = Encoding.UTF8.GetBytes(report);
                    request.ContentLength = body.Length;
                    using (Stream stream = request.GetRequestStream()) stream.Write(body, 0, body.Length);
                    using (var response = (HttpWebResponse)request.GetResponse())
                    {
                        if ((int)response.StatusCode >= 300) return url + ": " + (int)response.StatusCode;
                    }
                    State("sending to " + url);
                    return null;
                }
                catch (WebException e)
                {
                    var response = e.Response as HttpWebResponse;
                    if (response == null) return url + ": " + e.Message;
                    string text = "";
                    try
                    {
                        using (var reader = new StreamReader(response.GetResponseStream(), Encoding.UTF8))
                        {
                            var buffer = new char[200];
                            text = new string(buffer, 0, reader.Read(buffer, 0, buffer.Length));
                        }
                    }
                    catch (Exception) { }
                    return url + ": " + (int)response.StatusCode + " " + text.Trim();
                }
                catch (Exception e)
                {
                    return url + ": " + e.Message;
                }
            }
        }

        // SerialLink is the NanoKVM's COM port, written with overlapped I/O:
        // a write the NanoKVM does not take (nothing reads the port there) is
        // cancelled after its time and the port stays usable. System.IO.Ports
        // blocks in that case, in the write or in the close after it, until
        // the device is unplugged.
        private sealed class SerialLink : IDisposable
        {
            public readonly string Name;
            private readonly SafeFileHandle handle;

            public SerialLink(string name)
            {
                Name = name;
                handle = CreateFile(@"\\.\" + name, 0xC0000000 /* read, write */, 0, IntPtr.Zero, 3 /* existing */,
                                    0x40000000 /* overlapped */, IntPtr.Zero);
                if (handle.IsInvalid) throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
                // The line settings are nominal (a USB byte stream); DTR tells
                // the NanoKVM's side that somebody is there.
                EscapeCommFunction(handle, 5 /* SETDTR */);
            }

            // Ready: the device's side has the port open. A CDC ACM device
            // reports that as DSR and carrier (the Linux gadget raises both
            // when its tty is opened and drops them when it is closed).
            public bool Ready()
            {
                uint status;
                if (!GetCommModemStatus(handle, out status)) throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
                return (status & (0x20 /* DSR */ | 0x80 /* carrier */)) != 0;
            }

            // Write: true when all of data went out within the time, false
            // when the NanoKVM did not take it. Throws when the port is gone.
            public bool Write(byte[] data, int milliseconds)
            {
                PurgeComm(handle, 0x0008 /* what the NanoKVM sent: nothing is expected */);
                var done = new ManualResetEvent(false);
                var overlapped = new NativeOverlapped { EventHandle = done.SafeWaitHandle.DangerousGetHandle() };
                GCHandle pinned = GCHandle.Alloc(data, GCHandleType.Pinned);
                IntPtr ov = Marshal.AllocHGlobal(Marshal.SizeOf(typeof(NativeOverlapped)));
                Marshal.StructureToPtr(overlapped, ov, false);
                bool finished = true;
                try
                {
                    int written;
                    if (WriteFile(handle, pinned.AddrOfPinnedObject(), data.Length, out written, ov)) return written == data.Length;
                    int error = Marshal.GetLastWin32Error();
                    if (error != 997 /* pending */) throw new System.ComponentModel.Win32Exception(error);
                    if (done.WaitOne(milliseconds))
                    {
                        if (!GetOverlappedResult(handle, ov, out written, false))
                            throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
                        return written == data.Length;
                    }
                    CancelIoEx(handle, ov);
                    PurgeComm(handle, 0x0001 | 0x0004 /* abort and drop what waits to go out */);
                    // The cancelled write must end before its memory goes.
                    finished = done.WaitOne(5000);
                    if (!finished) throw new IOException("the port does not cancel a write");
                    return false;
                }
                finally
                {
                    // A write that never ended still owns the buffers: they
                    // are left to it (the port is closed and found again).
                    if (finished)
                    {
                        Marshal.FreeHGlobal(ov);
                        pinned.Free();
                        done.Dispose();
                    }
                }
            }

            public void Dispose()
            {
                handle.Dispose();
            }

            [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
            private static extern SafeFileHandle CreateFile(string name, uint access, uint share, IntPtr security, uint disposition, uint flags, IntPtr template);

            [DllImport("kernel32.dll", SetLastError = true)]
            private static extern bool WriteFile(SafeFileHandle handle, IntPtr data, int count, out int written, IntPtr overlapped);

            [DllImport("kernel32.dll", SetLastError = true)]
            private static extern bool GetOverlappedResult(SafeFileHandle handle, IntPtr overlapped, out int transferred, bool wait);

            [DllImport("kernel32.dll", SetLastError = true)]
            private static extern bool CancelIoEx(SafeFileHandle handle, IntPtr overlapped);

            [DllImport("kernel32.dll", SetLastError = true)]
            private static extern bool PurgeComm(SafeFileHandle handle, uint flags);

            [DllImport("kernel32.dll", SetLastError = true)]
            private static extern bool GetCommModemStatus(SafeFileHandle handle, out uint status);

            [DllImport("kernel32.dll", SetLastError = true)]
            private static extern bool EscapeCommFunction(SafeFileHandle handle, uint function);
        }

        // ---- start with Windows -----------------------------------------

        // Install registers a task that runs this exe, where it is now, as
        // SYSTEM at every start of Windows, with the options given after
        // "install", and starts it.
        private static int Install(string[] args, Options o)
        {
            if (!IsElevated()) throw new InvalidOperationException("install needs an administrator's console");
            if (!o.NoDriver) InstallDriver(o.Yes);
            // The task runs a copy in Program Files: this exe is usually on
            // the NanoKVM's helper disk, which is read-only and goes away
            // with the USB composition.
            string exe = InstalledCopy();
            // An agent of an earlier install keeps the serial port, and the
            // copy it runs from cannot be replaced while it runs: end it.
            Schtasks("/End /TN \"" + TaskName + "\"", true);
            string self = Process.GetCurrentProcess().MainModule.FileName;
            if (!string.Equals(Path.GetFullPath(self), exe, StringComparison.OrdinalIgnoreCase))
            {
                Directory.CreateDirectory(Path.GetDirectoryName(exe));
                for (int attempt = 0; ; attempt++)
                {
                    try
                    {
                        File.Copy(self, exe, true);
                        break;
                    }
                    catch (IOException) when (attempt < 10)
                    {
                        Thread.Sleep(500); // the ended agent is still going
                    }
                }
            }
            var rest = args.Where(a => a != "install" && a != "--yes" && a != "--no-driver").Select(a => a.Contains(" ") ? "\"" + a + "\"" : a).ToList();
            if (o.LogFile == null)
            {
                rest.Add("--log");
                rest.Add("\"" + Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "NanoKVM", "nanokvm-sensors.log") + "\"");
            }
            // From a definition, not from schtasks' switches: a task made with
            // those is stopped after three days and does not run on battery.
            string xml = TaskXml.Replace("{exe}", System.Security.SecurityElement.Escape(exe))
                                .Replace("{args}", System.Security.SecurityElement.Escape(string.Join(" ", rest)));
            string file = Path.Combine(Path.GetTempPath(), "nanokvm-sensors-task-" + Process.GetCurrentProcess().Id + ".xml");
            try
            {
                File.WriteAllText(file, xml, Encoding.Unicode);
                int code = Schtasks("/Create /F /TN \"" + TaskName + "\" /XML \"" + file + "\"");
                if (code != 0) return code;
                return Schtasks("/Run /TN \"" + TaskName + "\"");
            }
            finally
            {
                try { File.Delete(file); } catch (Exception) { }
            }
        }

        // Where the installed agent lives.
        private static string InstalledCopy()
        {
            return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles), "NanoKVM", "nanokvm-sensors.exe");
        }

        // InstallDriver runs the PawnIO installer this program carries, as
        // LibreHardwareMonitor does, when the driver is missing or too old
        // for the library. The driver is a system component: asked first.
        private static void InstallDriver(bool yes)
        {
            Version have = PawnIoVersion();
            if (have != null && have >= new Version(2, 0, 0, 0)) return;
            using (Stream setup = Assembly.GetExecutingAssembly().GetManifestResourceStream("pawnio/PawnIO_setup.exe"))
            {
                if (setup == null)
                {
                    Console.WriteLine("The PawnIO driver is " + (have == null ? "not installed" : "older than 2.0") +
                                      " and this build carries no installer: get it from https://pawnio.eu");
                    return;
                }
                if (!yes)
                {
                    Console.Write("The PawnIO driver (processor and board sensors) is " +
                                  (have == null ? "not installed" : "version " + have + ", older than 2.0") + ". Install it now? [Y/n] ");
                    string answer = (Console.ReadLine() ?? "n").Trim().ToLowerInvariant();
                    if (answer != "" && answer != "y" && answer != "yes") return;
                }
                string path = Path.Combine(Path.GetTempPath(), "PawnIO_setup-" + Process.GetCurrentProcess().Id + ".exe");
                try
                {
                    using (FileStream file = File.Create(path)) setup.CopyTo(file);
                    using (Process p = Process.Start(new ProcessStartInfo(path, "-install") { UseShellExecute = false }))
                    {
                        p.WaitForExit();
                        Console.WriteLine(p.ExitCode == 0 ? "PawnIO installed." : "The PawnIO installer ended with code " + p.ExitCode + ".");
                    }
                }
                finally
                {
                    try { File.Delete(path); } catch (Exception) { }
                }
            }
        }

        // The task: at every start of Windows, as SYSTEM, without a time
        // limit, on battery too, started again a minute after a failure.
        private const string TaskXml =
@"<?xml version=""1.0"" encoding=""UTF-16""?>
<Task version=""1.2"" xmlns=""http://schemas.microsoft.com/windows/2004/02/mit/task"">
  <RegistrationInfo>
    <Description>Sends this computer's sensors to the NanoKVM plugged into it (NanoKVM OS, nanokvm-sensors).</Description>
  </RegistrationInfo>
  <Triggers>
    <BootTrigger>
      <Enabled>true</Enabled>
    </BootTrigger>
  </Triggers>
  <Principals>
    <Principal id=""Author"">
      <UserId>S-1-5-18</UserId>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context=""Author"">
    <Exec>
      <Command>{exe}</Command>
      <Arguments>{args}</Arguments>
    </Exec>
  </Actions>
</Task>";

        private static int Schtasks(string arguments, bool quiet = false)
        {
            var info = new ProcessStartInfo(Path.Combine(Environment.SystemDirectory, "schtasks.exe"), arguments)
            {
                UseShellExecute = false, RedirectStandardOutput = quiet, RedirectStandardError = quiet
            };
            using (Process p = Process.Start(info))
            {
                if (quiet)
                {
                    p.StandardOutput.ReadToEnd();
                    p.StandardError.ReadToEnd();
                }
                p.WaitForExit();
                return p.ExitCode;
            }
        }
    }
}
