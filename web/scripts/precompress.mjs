// Compress at build time: the device only selects and serves a ready file.
import { readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { brotliCompressSync, constants, gzipSync } from 'node:zlib';

const compressible = /\.(js|css|svg|json|wasm)$/;
const encoders = {
  gz: (data) => gzipSync(data, { level: constants.Z_BEST_COMPRESSION }),
  br: (data) =>
    brotliCompressSync(data, {
      params: { [constants.BROTLI_PARAM_QUALITY]: constants.BROTLI_MAX_QUALITY }
    })
};

export function precompressAssets(root) {
  const totals = { gz: { files: 0, before: 0, after: 0 }, br: { files: 0, before: 0, after: 0 } };
  for (const name of readdirSync(root)) {
    const path = join(root, name);
    if (!compressible.test(name) || !statSync(path).isFile()) continue;
    const data = readFileSync(path);
    for (const [extension, encode] of Object.entries(encoders)) {
      const target = `${path}.${extension}`;
      // Re-running after an asset changes must not leave a stale variant.
      rmSync(target, { force: true });
      if (data.length < 1024) continue;
      const compressed = encode(data);
      if (compressed.length > data.length * 0.9) continue;
      writeFileSync(target, compressed);
      totals[extension].files++;
      totals[extension].before += data.length;
      totals[extension].after += compressed.length;
    }
  }
  return totals;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const totals = precompressAssets(fileURLToPath(new URL('../dist/assets/', import.meta.url)));
  for (const [extension, { files, before, after }] of Object.entries(totals))
    console.log(
      `precompressed ${extension}: ${files} assets, ${Math.round(before / 1024)} KiB -> ${Math.round(after / 1024)} KiB`
    );
}
