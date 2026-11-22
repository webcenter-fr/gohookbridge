import { cpSync, rmSync, existsSync } from 'node:fs'

const src = '.output/public'
const dst = '../internal/web/static'

if (!existsSync(src)) {
  console.error('ERROR: .output/public is missing. Did "nuxt generate" run?')
  process.exit(1)
}

// internal/web/handler.go embeds the output with the "all:static" directive,
// which includes files/dirs starting with '_' or '.' at any depth (e.g. Vite
// chunks like assets/_LzBFbKv.js). No exclusion guard is needed here anymore.
rmSync(dst, { recursive: true, force: true })
cpSync(src, dst, { recursive: true })
console.log(`Copied ${src} → ${dst}`)
