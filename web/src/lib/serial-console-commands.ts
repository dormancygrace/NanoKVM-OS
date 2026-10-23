// The commands the serial console panel offers for the managed computer.

export const serialConsoleScript = 'nanokvm-serial-console.sh';

export type SerialConsoleIdentity = {
  serialNumber: string;
  vendorId?: string;
  productId?: string;
};

// How the managed computer fetches the script from this NanoKVM: from the
// address the browser uses, or from the NanoKVM's USB network address when
// the computer has no other network. The certificate is self-signed (-k),
// so the command checks the script against the hash this page got over the
// browser's connection before running it as root.
export function fetchCommands(
  origin: { https: boolean; browser: string },
  usbAddress: string | undefined,
  sha256: string,
  args: string
) {
  const { https } = origin;
  const run = `echo '${sha256}  ${serialConsoleScript}' | sha256sum -c - && sh ${serialConsoleScript}${args}`;
  const curl = (from: string) =>
    `curl -fsS${https ? ' -k' : ''} ${from}/${serialConsoleScript} -o ${serialConsoleScript} && ${run}`;
  const wget = (from: string) =>
    `wget -q${https ? ' --no-check-certificate' : ''} ${from}/${serialConsoleScript} -O ${serialConsoleScript} && ${run}`;
  const browserOrigin = origin.browser;
  const usbOrigin = usbAddress ? `${https ? 'https' : 'http'}://${usbAddress}` : undefined;
  return {
    network: curl(browserOrigin),
    usb: usbOrigin ? curl(usbOrigin) : undefined,
    wget: wget(usbOrigin ?? browserOrigin)
  };
}

// The script's arguments for this NanoKVM: its serial number, and its IDs
// when they are not the factory 3346:1009 the script assumes.
export function scriptArgs(status?: SerialConsoleIdentity) {
  if (!status) return '';
  let args = status.serialNumber ? ` --serial ${status.serialNumber}` : '';
  if (
    status.vendorId &&
    status.productId &&
    `${status.vendorId}:${status.productId}` !== '3346:1009'
  ) {
    args += ` --vid ${status.vendorId} --pid ${status.productId}`;
  }
  return args;
}
