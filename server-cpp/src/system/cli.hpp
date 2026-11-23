#pragma once

// The command-line modes of NanoKVM-Server, as the Go server's main.go: "totp-reset USER" (nkos-totp-reset: turns
// two-factor authentication off for one web account, from the console) and "factory-reset" (kvm_system runs it when
// the BOOT button is held for 9 seconds: the settings are reset as Settings > Factory reset does without "Keep
// users" and with "Keep apps", then the device reboots).

namespace nkos::system {

// The exit status of the mode argv names, or -1 when argv is not a command-line mode (the server starts).
int run_cli(int argc, char **argv);

} // namespace nkos::system
