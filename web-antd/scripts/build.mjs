/* =================================================================
 *  web-antd / scripts / build.mjs
 *  把 React + Ant Design 单页应用打成 app.js + app.css，并同步到
 *  server/internal/api/assets_web/（Go 通过 go:embed 内嵌）。
 *
 *  用法：
 *    node scripts/build.mjs            # 构建并同步
 *    node scripts/build.mjs --report   # 构建并打印体积报告
 *    node scripts/build.mjs --watch    # 监听源码增量重建
 * ================================================================= */
import { build, context } from 'esbuild';
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(here, '..');
const repoRoot = path.resolve(webRoot, '..');
const distDir = path.join(webRoot, 'dist');
const goAssetsDir = path.join(repoRoot, 'server', 'internal', 'api', 'assets_web');
const pkg = JSON.parse(fs.readFileSync(path.join(webRoot, 'package.json'), 'utf8'));

const argv = new Set(process.argv.slice(2));
const watch = argv.has('--watch');
const report = argv.has('--report') || !watch;

fs.mkdirSync(distDir, { recursive: true });

const gz = (buf) => zlib.gzipSync(buf, { level: 9 }).length;
const kb = (n) => (n / 1024).toFixed(1) + ' KB';
const stamp = () => new Date().toISOString().replace('T', ' ').slice(0, 19) + ' UTC';

const appOptions = {
  entryPoints: [path.join(webRoot, 'src/main.jsx')],
  outdir: distDir,
  entryNames: 'app',
  bundle: true,
  minify: true,
  format: 'iife',
  target: ['es2020'],
  jsx: 'automatic',
  loader: { '.js': 'jsx', '.jsx': 'jsx', '.svg': 'text', '.png': 'dataurl' },
  define: { 'process.env.NODE_ENV': '"production"' },
  legalComments: 'none',
  charset: 'utf8',
  treeShaking: true,
  logLevel: 'warning',
  metafile: true,
};

function syncGoAssets() {
  const jsPath = path.join(distDir, 'app.js');
  const cssPath = path.join(distDir, 'app.css');
  const jsBuf = fs.readFileSync(jsPath);
  const cssBuf = fs.existsSync(cssPath) ? fs.readFileSync(cssPath) : Buffer.from('');
  if (fs.existsSync(goAssetsDir)) {
    fs.copyFileSync(jsPath, path.join(goAssetsDir, 'app.js'));
    fs.copyFileSync(cssPath, path.join(goAssetsDir, 'app.css'));
    // 预压缩资源：Go 直接嵌入并返回，避免 Cloudflare 回源时传输未压缩的 2 MB JS。
    const writeCompressed = (name, buf) => {
      fs.writeFileSync(path.join(goAssetsDir, name + '.br'), zlib.brotliCompressSync(buf, {
        params: { [zlib.constants.BROTLI_PARAM_QUALITY]: 11 },
      }));
      fs.writeFileSync(path.join(goAssetsDir, name + '.gz'), zlib.gzipSync(buf, { level: 9 }));
    };
    writeCompressed('app.js', jsBuf);
    writeCompressed('app.css', cssBuf);
    const g6Path = path.join(distDir, 'g6-app.js');
    if (fs.existsSync(g6Path)) {
      const g6Buf = fs.readFileSync(g6Path);
      fs.copyFileSync(g6Path, path.join(goAssetsDir, 'g6-app.js'));
      writeCompressed('g6-app.js', g6Buf);
    }
    const xtermPath = path.join(distDir, 'xterm-app.js');
    if (fs.existsSync(xtermPath)) {
      const xtermBuf = fs.readFileSync(xtermPath);
      fs.copyFileSync(xtermPath, path.join(goAssetsDir, 'xterm-app.js'));
      writeCompressed('xterm-app.js', xtermBuf);
    }
    // 版本号优先取服务端 main.go（唯一版本源），避免前端 meta.json 落后于后端
    const goMain = path.join(here, '../../server/cmd/proxypilot/main.go');
    const goVersion = fs.existsSync(goMain)
      ? (fs.readFileSync(goMain, 'utf8').match(/var version\s*=\s*\"([^\"]+)\"/) || [])[1]
      : '';
    const rawVersion = (goVersion || String(pkg.version || '15.0.0')).replace(/^v/i, '');
    const version = 'V' + rawVersion.split('.').slice(0, 2).join('.');
    fs.writeFileSync(
      path.join(goAssetsDir, 'meta.json'),
      JSON.stringify({ version, builtAt: stamp() }, null, 2) + '\n',
      'utf8'
    );
  }
  return { jsBuf, cssBuf };
}

function reportBuild(jsBuf, cssBuf, result) {
  const pad = (s, n) => String(s).padEnd(n);
  const num = (n) => String(n).padStart(11);
  console.log('\n  构建完成 — ' + stamp());
  console.log('  ' + pad('产物', 26) + pad('原始', 12) + 'gzip');
  console.log('  ' + '-'.repeat(56));
  for (const [name, buf] of [
    ['dist/app.js', jsBuf],
    ['dist/app.css', cssBuf],
  ]) {
    console.log('  ' + pad(name, 26) + num(kb(buf.length)) + '  ' + kb(gz(buf)));
  }
  console.log('  ' + '-'.repeat(56));
  const meta = result && result.metafile && Object.values(result.metafile.outputs)[0];
  if (meta) {
    const top = Object.entries(meta.inputs)
      .sort((a, b) => b[1].bytesInOutput - a[1].bytesInOutput)
      .slice(0, 8)
      .map(([k, v]) => '    ' + pad(path.relative(webRoot, k), 44) + kb(v.bytesInOutput));
    if (top.length) console.log('\n  体积最大的 8 个模块：\n' + top.join('\n') + '\n');
  }
}

async function runBuild() {
  const result = await build(appOptions);
  // xterm 只在打开远程终端时需要，单独成包，避免阻塞首屏。
  await build({
    entryPoints: [path.join(webRoot, 'src/xterm-entry.js')],
    outfile: path.join(distDir, 'xterm-app.js'),
    bundle: true,
    minify: true,
    format: 'iife',
    target: ['es2020'],
    define: { 'process.env.NODE_ENV': '"production"' },
    legalComments: 'none',
    charset: 'utf8',
    treeShaking: true,
    logLevel: 'warning',
  });
  // AntV G6 仅在流量流向图使用：单独成包，按需加载
  await build({
    entryPoints: [path.join(webRoot, 'src/g6-entry.js')],
    outfile: path.join(distDir, 'g6-app.js'),
    bundle: true,
    minify: true,
    format: 'iife',
    target: ['es2020'],
    define: { 'process.env.NODE_ENV': '"production"' },
    legalComments: 'none',
    charset: 'utf8',
    logLevel: 'warning',
  });
  const { jsBuf, cssBuf } = syncGoAssets();
  if (report) reportBuild(jsBuf, cssBuf, result);
  else console.log('  构建完成 — ' + stamp());
}

if (watch) {
  const ctx = await context({
    ...appOptions,
    plugins: [
      {
        name: 'sync-go-assets',
        setup(b) {
          b.onEnd(() => {
            try {
              const { jsBuf, cssBuf } = syncGoAssets();
              console.log('  [' + stamp() + '] app.js ' + kb(jsBuf.length) + ' / app.css ' + kb(cssBuf.length));
            } catch (e) {
              console.error('同步 Go 资源失败：', e.message);
            }
          });
        },
      },
    ],
  });
  await ctx.watch();
  console.log('[watch] 已监听 web-antd/src，改动后自动重建并同步到 server/assets_web…');
} else {
  await runBuild();
}
