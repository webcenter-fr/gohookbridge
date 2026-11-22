import { defineVitestConfig } from '@nuxt/test-utils/config'

export default defineVitestConfig({
  test: {
    environment: 'nuxt',
    // Run test files sequentially. With parallel workers, @nuxt/test-utils'
    // virtual app entry (nuxt-vitest-app-entry.js) intermittently fails to
    // resolve its "./diagnostics/core.js" import, failing whole files. The
    // suite is small enough that sequential execution is still fast.
    fileParallelism: false,
  },
})
