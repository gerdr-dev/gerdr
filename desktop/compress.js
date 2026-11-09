// node desktop/compress.js <in> <out>: brotli, the format the updaters unpack (internal/selfupdate).
// Brotli rather than gzip: the backend comes out ~25% smaller, and Node / Go unpack it without anything extra.
// Quality 10, not the maximum 11: measured on the 0.2.25 arm64 backend (42MB), 11 gives 10.18MB in 80s, 10 gives
// 10.65MB in 42s (+5%, half the time), 9 gives 11.80MB in 15s (+16%). The release waits on these.
const fs = require("node:fs");
const zlib = require("node:zlib");
const [, , src, dst] = process.argv;
const b = fs.readFileSync(src);
const { BROTLI_PARAM_QUALITY, BROTLI_PARAM_LGWIN, BROTLI_PARAM_SIZE_HINT } = zlib.constants;
fs.writeFileSync(dst, zlib.brotliCompressSync(b, { params: { [BROTLI_PARAM_QUALITY]: 10, [BROTLI_PARAM_LGWIN]: 24, [BROTLI_PARAM_SIZE_HINT]: b.length } }));
