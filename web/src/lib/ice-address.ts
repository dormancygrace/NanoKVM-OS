// host:port with an optional stun:/turn: scheme, as the server accepts it.
export function validIceAddress(value: string) {
  const address = value.trim().replace(/^(stun|turn):/, '');
  const match = /^(\[[0-9a-fA-F:.]+\]|[A-Za-z0-9.-]+):(\d{1,5})$/.exec(address);
  if (!match) return false;
  const port = Number(match[2]);
  return port >= 1 && port <= 65535;
}
