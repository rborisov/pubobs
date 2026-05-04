import esbuild from 'esbuild';
import process from 'process';

const prod = process.argv[2] === 'production';

await esbuild.build({
  entryPoints: ['src/main.ts'],
  bundle: true,
  format: 'iife',
  target: 'es2018',
  outfile: '../backend/frontend/static/app.js',
  sourcemap: prod ? false : 'inline',
  minify: prod,
  logLevel: 'info',
});
