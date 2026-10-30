// node desktop/compress.js <in> <out>: brotli at max quality, the format desktop/updater.js unpacks.
// Brotli rather than gzip: the backend comes out ~25% smaller (37MB → ~9.7MB vs ~13MB), and Node unpacks it
// without anything extra (xz would be a little smaller still, but Node can't read it). Takes about a minute.
const fs = require("node:fs");
const zlib = require("node:zlib");
const [, , src, dst] = process.argv;
const b = fs.readFileSync(src);
const { BROTLI_PARAM_QUALITY, BROTLI_PARAM_LGWIN, BROTLI_PARAM_SIZE_HINT } = zlib.constants;
fs.writeFileSync(dst, zlib.brotliCompressSync(b, { params: { [BROTLI_PARAM_QUALITY]: 11, [BROTLI_PARAM_LGWIN]: 24, [BROTLI_PARAM_SIZE_HINT]: b.length } }));
