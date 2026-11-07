const { build } = require('esbuild');
const fs = require('fs');
const zlib = require('zlib');

build({
  entryPoints: ['src/g6-entry.js'],
  outfile: 'dist/g6-app.js',
  bundle: true,
  minify: true,
  format: 'iife',
  target: ['es2020'],
  define: { 'process.env.NODE_ENV': '"production"' },
  legalComments: 'none',
  logLevel: 'warning',
})
  .then(() => {
    const b = fs.readFileSync('dist/g6-app.js');
    console.log('g6-app.js', (b.length / 1024).toFixed(1) + 'KB', 'gzip', (zlib.gzipSync(b, { level: 9 }).length / 1024).toFixed(1) + 'KB');
  })
  .catch((e) => {
    console.error('BUILD FAIL', e.message);
    process.exit(1);
  });
