/*
 * The monitor identity the EDID profiles carry instead of NanoKVM's own
 * (server/common/monitor_identity.go): what a computer shows for the monitor
 * and keys its per-monitor settings to. The modes stay NanoKVM's.
 */

export type MonitorIdentity = {
  // Three-letter PNP ID, such as ACR or DEL.
  manufacturer: string;
  productCode: number;
  // The monitor name descriptor, up to 13 printable ASCII characters.
  name: string;
  // The numeric serial of every profile; null keeps each profile's own, so
  // Windows does not carry a display mode from one profile to the next.
  serialNumber: number | null;
  // The serial number descriptor with serialNumber; empty: the number.
  serialText: string;
  // Week of manufacture: 0 unknown, 255 when year is the model year.
  week: number;
  year: number;
};

// A form field that does not hold a valid value.
export type IdentityField =
  'manufacturer' | 'productCode' | 'name' | 'serialNumber' | 'serialText' | 'week' | 'year';

const TEXT_LENGTH = 13;
const printable = (s: string) => /^[\x20-\x7e]*$/.test(s);

// The draft value: '' is NanoKVM's own identity, otherwise the identity as
// JSON with a fixed key order, so equal identities compare equal. Fields the
// form cannot parse are null, a fixed serial number it cannot parse -1 (null
// there means each profile's own).
export function identityKey(identity: MonitorIdentity | null | undefined): string {
  if (!identity) return '';
  return JSON.stringify({
    manufacturer: identity.manufacturer,
    productCode: Number.isFinite(identity.productCode) ? identity.productCode : null,
    name: identity.name,
    serialNumber:
      identity.serialNumber === null || Number.isFinite(identity.serialNumber)
        ? identity.serialNumber
        : -1,
    serialText: identity.serialText,
    week: Number.isFinite(identity.week) ? identity.week : null,
    year: Number.isFinite(identity.year) ? identity.year : null
  });
}

export function identityFromKey(key: string): MonitorIdentity | null {
  if (!key) return null;
  const value = JSON.parse(key) as Record<string, unknown>;
  const num = (v: unknown) => (typeof v === 'number' ? v : NaN);
  return {
    manufacturer: String(value.manufacturer ?? ''),
    productCode: num(value.productCode),
    name: String(value.name ?? ''),
    serialNumber: value.serialNumber === null ? null : num(value.serialNumber),
    serialText: String(value.serialText ?? ''),
    week: num(value.week),
    year: num(value.year)
  };
}

// A new identity to fill in: per-profile serial numbers, this year.
export function blankIdentity(year = new Date().getFullYear()): MonitorIdentity {
  return {
    manufacturer: '',
    productCode: 0,
    name: '',
    serialNumber: null,
    serialText: '',
    week: 0,
    year
  };
}

const integer = (v: number, min: number, max: number) =>
  Number.isInteger(v) && v >= min && v <= max;

// The first invalid field, in form order; undefined when the identity is valid.
// The same rules as MonitorIdentity.Validate on the server.
export function identityIssue(identity: MonitorIdentity): IdentityField | undefined {
  if (!/^[A-Z]{3}$/.test(identity.manufacturer)) return 'manufacturer';
  if (!integer(identity.productCode, 0, 0xffff)) return 'productCode';
  if (!identity.name || identity.name.length > TEXT_LENGTH || !printable(identity.name))
    return 'name';
  if (identity.serialNumber !== null && !integer(identity.serialNumber, 0, 0xffffffff))
    return 'serialNumber';
  if (
    identity.serialText.length > TEXT_LENGTH ||
    !printable(identity.serialText) ||
    (identity.serialText && identity.serialNumber === null)
  )
    return 'serialText';
  if (!integer(identity.week, 0, 54) && identity.week !== 255) return 'week';
  if (!integer(identity.year, 1990, 2245)) return 'year';
  return undefined;
}

// The monitorIdentity field of POST /api/vm/video.
export function identityRequest(key: string) {
  const identity = identityFromKey(key);
  return identity ? { custom: true, ...identity } : { custom: false };
}

// The product code as the four hex digits a monitor's hardware ID shows.
export const productHex = (code: number) => code.toString(16).toUpperCase().padStart(4, '0');

// The 128-byte base block of an EDID file: binary (.bin, as Linux's
// /sys/class/drm/*/edid or CRU save it) or a text hex dump.
function baseBlock(data: Uint8Array): Uint8Array | undefined {
  const header = [0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0];
  const at = (bytes: ArrayLike<number>, i: number) => header.every((b, j) => bytes[i + j] === b);
  if (data.length >= 128 && at(data, 0)) return data.slice(0, 128);
  const text = new TextDecoder('latin1').decode(data);
  for (const tokens of [
    text.match(/\b[0-9a-fA-F]{2}\b/g) ?? [],
    text.replace(/[^0-9a-fA-F]/g, '').match(/../g) ?? []
  ]) {
    const bytes = tokens.map((t) => parseInt(t, 16));
    for (let i = 0; i + 128 <= bytes.length; i++) {
      if (at(bytes, i)) return Uint8Array.from(bytes.slice(i, i + 128));
    }
  }
  return undefined;
}

export type EDIDImport =
  { ok: true; identity: MonitorIdentity } | { ok: false; error: 'not-edid' | 'checksum' };

// The identity of an EDID file. The serial number is kept: the identity is
// that monitor's, as a headless workstation expects.
export function identityFromEDID(data: Uint8Array): EDIDImport {
  const block = baseBlock(data);
  if (!block) return { ok: false, error: 'not-edid' };
  if (block.reduce((sum, b) => (sum + b) & 0xff, 0) !== 0) return { ok: false, error: 'checksum' };
  const id = (block[8] << 8) | block[9];
  const manufacturer = [10, 5, 0]
    .map((shift) => String.fromCharCode(((id >> shift) & 31) + 64))
    .join('');
  const text = (offset: number) =>
    String.fromCharCode(...block.slice(offset + 5, offset + 18))
      .split('\n')[0]
      .replace(/[^\x20-\x7e]/g, '')
      .trimEnd();
  let name = '';
  let serialText = '';
  for (let offset = 54; offset + 18 <= 126; offset += 18) {
    if (block[offset] || block[offset + 1] || block[offset + 2]) continue; // a timing
    if (block[offset + 3] === 0xfc && !name) name = text(offset);
    if (block[offset + 3] === 0xff && !serialText) serialText = text(offset);
  }
  const serialNumber = (block[12] | (block[13] << 8) | (block[14] << 16) | (block[15] << 24)) >>> 0;
  return {
    ok: true,
    identity: {
      manufacturer,
      productCode: block[10] | (block[11] << 8),
      name,
      serialNumber,
      serialText,
      week: block[16] <= 54 || block[16] === 255 ? block[16] : 0,
      year: 1990 + block[17]
    }
  };
}
