// Transport budgets for add-on calls whose server side runs apk or a slow
// CLI on the device. They are longer than the server's own command limits;
// a transport timeout means the result is unknown, not that it failed.

// A package install downloads over whatever uplink the device has.
export const DOWNLOAD_TIMEOUT = 10 * 60 * 1000;

// Uninstall, start/stop and CLI calls that can take a while on the device.
export const SLOW_CALL_TIMEOUT = 180 * 1000;
