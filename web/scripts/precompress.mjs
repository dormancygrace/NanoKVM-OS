// Store a .gz next to each compressible build asset. The device serves them
// (server/router/assets.go) instead of compressing on its single slow core;
// the uncompressed file stays for clients without gzip.
import { readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { constants, gzipSync } from 'node:zlib';

const root = new URL('../dist/assets/', import.meta.url).pathname;
const compressible = /\.(js|css|svg|json|wasm)$/;

let before = 0;
let after = 0;
for (const name of readdirSync(root)) {
  const path = join(root, name);
  if (!compressible.test(name) || !statSync(path).isFile()) continue;
  const data = readFileSync(path);
  // zlib writes no file name and a zero timestamp: the output is reproducible.
  const gz = gzipSync(data, { level: constants.Z_BEST_COMPRESSION });
  if (data.length < 1024 || gz.length > data.length * 0.9) continue;
  writeFileSync(`${path}.gz`, gz);
  before += data.length;
  after += gz.length;
}
console.log(
  `precompressed assets: ${Math.round(before / 1024)} KiB -> ${Math.round(after / 1024)} KiB`
);
