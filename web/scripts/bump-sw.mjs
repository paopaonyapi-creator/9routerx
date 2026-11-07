/**
 * Post-build step: rewrites the service worker's cache name in dist/sw.js so
 * every build gets a unique cache namespace. Without this, returning browsers
 * kept serving the previous bundle from `9routerx-static-v3` forever — deploys
 * never reached users whose SW was already installed (audit finding).
 *
 * The name is derived from the hashed entry asset in dist/index.html, so two
 * builds with identical content share a cache and re-runs stay stable.
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { createHash } from 'node:crypto'

const index = readFileSync('dist/index.html', 'utf-8')
const assets = [...index.matchAll(/assets\/[A-Za-z0-9._-]+/g)].map((m) => m[0]).sort()
const digest = createHash('sha256').update(assets.join('|')).digest('hex').slice(0, 12)
const swPath = 'dist/sw.js'
let sw = readFileSync(swPath, 'utf-8')
sw = sw.replace(/const CACHE_NAME = '[^']*'/, `const CACHE_NAME = '9routerx-static-${digest}'`)
writeFileSync(swPath, sw)
console.log(`[bump-sw] cache name -> 9routerx-static-${digest}`)
