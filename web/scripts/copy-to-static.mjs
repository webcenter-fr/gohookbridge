import { cpSync, rmSync, existsSync, readdirSync } from 'node:fs'

const src = '.output/public'
const dst = '../internal/web/static'

if (!existsSync(src)) {
  console.error('ERROR: .output/public is missing. Did "nuxt generate" run?')
  process.exit(1)
}

// CRITICAL: Go's go:embed silently excludes files/dirs starting with '_' or '.'.
// If Nuxt ever changes its output to include such entries, the embedded binary
// would be silently broken. Fail the build loudly if any such entry exists.
const forbidden = readdirSync(src).filter(e => e.startsWith('_') || e.startsWith('.'))
if (forbidden.length > 0) {
  console.error(`ERROR: .output/public contains entries that go:embed would exclude: ${forbidden.join(', ')}`)
  console.error('These files would be silently missing from the Go binary.')
  console.error('Check nuxt.config.ts app.buildAssetsDir — it must NOT start with _ or .')
  process.exit(1)
}

rmSync(dst, { recursive: true, force: true })
cpSync(src, dst, { recursive: true })
console.log(`Copied ${src} → ${dst}`)
