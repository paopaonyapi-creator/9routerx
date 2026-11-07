// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const appVersion = (() => {
  try {
    return fs.readFileSync(path.resolve(__dirname, '../VERSION'), 'utf-8').trim()
  } catch {
    return ''
  }
})()

export default defineConfig({
  define: {
    __APP_VERSION__: JSON.stringify(appVersion),
  },
  plugins: [svelte(), tailwindcss()],
  /* via shadcn-svelte (MIT): $lib alias required by the shadcn-svelte Vite install */
  resolve: {
    alias: {
      $lib: path.resolve(__dirname, './src/lib'),
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:888',
      '/v1': 'http://localhost:888',
      '/usage': 'http://localhost:888',
      '/translator': 'http://localhost:888',
      '/debug': 'http://localhost:888',
      '/admin': 'http://localhost:888',
    },
  },
})
