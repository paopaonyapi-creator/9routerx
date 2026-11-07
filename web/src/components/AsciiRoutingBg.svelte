<!-- 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  import { onMount } from 'svelte'
  import { onRoutingBurst } from '../lib/routingBurst'

  /**
   * "Live Routing Flow" background (TASK-9routerX-anim-bg).
   *
   * One <canvas>, same seeded ASCII routing topology as the old static <pre>
   * (provider-node clusters joined by L-shaped route runs), animated in ONE
   * rAF loop: 2.1 packet pulses travel the real route polylines (accent
   * tiles, fading trail, per-pulse duration + sinusoidal speed easing);
   * 2.2 per-node heartbeat brightness (+-FLOW.heartbeatAmplitude, 2-6s
   * periods/phases) with an orange 300ms node flash when a pulse passes;
   * 2.3 glyph shuffle on non-route tiles (2/s desktop, 0.5/s mobile);
   * 2.5 pointer/touch sonar trail, action bursts via the routingBurst() bus
   * (wired: toasts + login submit success), tap ripple on empty background.
   *
   * FIRST-PAINT STRATEGY (the previous canvas attempt died here): mount-time
   * layout is not guaranteed to be measurable, so painting never trusts a
   * single pass. We (a) attempt a synchronous paint, (b) schedule a
   * double-RAF paint (one full layout+style cycle later), (c) ResizeObserver
   * re-paints whenever the element gets a real box, and (d) an explicit
   * retry loop re-attempts up to ~1.5s, each attempt VERIFYING that pixels
   * actually landed (getImageData alpha probe on a known glyph tile) before
   * declaring success. Any of the four paths wins; the rest are no-ops.
   *
   * PERFORMANCE GUARDS (spec §3): dirty-rect redraws only (the gridline
   * underlay is CSS on the <canvas>, so clearRect reveals it for free; full
   * redraws only on resize/theme change/font load); DPR capped at 2; animated
   * tiles per frame far below the 10% budget (dirty-list overflow degrades
   * to one full redraw); mobile throttled to FLOW.mobileFpsCap via timestamp;
   * document.hidden CANCELS the loop (visibilitychange restarts it);
   * prefers-reduced-motion renders the static map only; zero object
   * allocation in the hot loop (pulses, bursts, ripples, pointer points and
   * the dirty/fx lists are pre-allocated pools).
   */

  // ---- single central config (spec §3 example + documented extras) ----
  const FLOW = {
    pulseCountDesktop: 4, // 3-6
    pulseCountMobile: 2, // 1-2
    pulseMinDurationMs: 2200,
    pulseMaxDurationMs: 4200,
    trailTiles: 3,
    heartbeatPeriodMinMs: 2000,
    heartbeatPeriodMaxMs: 6000,
    heartbeatAmplitude: 0.18, // +/- brightness
    nodeFlashMs: 300,
    shuffleRateDesktopPerSec: 2,
    shuffleRateMobilePerSec: 0.5,
    mobileFpsCap: 30,
    // extras (not in the spec example, tuned here only):
    baseOpacity: 0.14, // overall tile alpha (old <pre> used 0.12 CSS)
    cellFallbackPx: 12,
    dprCap: 2,
    maxEffects: 3, // simultaneous bursts+ripples (spec 2.5.5)
    burstSprayTiles: 8, // 6-10 per spec
    burstLifeMs: 600,
    rippleRings: 3,
    rippleLifeMs: 900,
    rippleMaxRadius: 6, // tiles
    pointerPool: 16,
    pointerFadeMs: 700, // per-point sonar fade; idle simply stops lighting
  }

  // ---- seeded topology (unchanged map-generation logic from the <pre>) ----
  function mulberry32(seed: number): () => number {
    let a = seed >>> 0
    return () => {
      a = (a + 0x6d2b79f5) | 0
      let t = Math.imul(a ^ (a >>> 15), 1 | a)
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296
    }
  }

  const SEED = 0x39780
  const TILE_CHARS = ['x', 'S', '%', 't', 'o', '8']
  const H_ROUTE = ['─', '═', '┄']
  const V_ROUTE = ['│', '┆']
  const DOT = '·'
  const ACCENT_CHARS = ['x', '8', 'o']

  interface MapNode {
    x: number
    y: number
    r: number
    periodMs: number
    phase: number
    tiles: number[] // tile indices of the cluster (heartbeat scope)
  }

  interface World {
    cols: number
    rows: number
    n: number
    chars: string[]
    kind: Uint8Array // 0 empty, 1 speck, 2 route, 3 cluster, 4 accent
    nodeId: Int16Array
    nodes: MapNode[]
    routes: number[][] // tile-index polylines pulses travel along
    shufflable: Int32Array // speck/cluster (non-route, non-accent) tiles
    probeTile: number // a tile that must have painted pixels
  }

  interface Pulse {
    active: boolean
    burst: boolean // single-shot fast pulse from an action burst
    path: number[] | null
    pos: number
    dir: number
    tilesPerSec: number
    wf: number // speed-easing angular frequency
    wp: number // speed-easing phase
  }

  function pick<T>(rng: () => number, arr: T[]): T {
    return arr[Math.floor(rng() * arr.length)]
  }

  /** Prim's MST over Manhattan distance -> index pairs. */
  function primEdges(nodes: MapNode[]): [number, number][] {
    const n = nodes.length
    if (n < 2) return []
    const inTree = new Array<boolean>(n).fill(false)
    inTree[0] = true
    const edges: [number, number][] = []
    for (let added = 1; added < n; added++) {
      let best = -1
      let bestFrom = 0
      let bestDist = Infinity
      for (let i = 0; i < n; i++) {
        if (!inTree[i]) continue
        for (let j = 0; j < n; j++) {
          if (inTree[j]) continue
          const d = Math.abs(nodes[i].x - nodes[j].x) + Math.abs(nodes[i].y - nodes[j].y)
          if (d < bestDist) {
            bestDist = d
            best = j
            bestFrom = i
          }
        }
      }
      if (best === -1) break
      inTree[best] = true
      edges.push([bestFrom, best])
    }
    return edges
  }

  function generateWorld(cols: number, rows: number): World {
    const rng = mulberry32(SEED)
    const n = cols * rows
    const chars: string[] = new Array(n).fill('')
    const kind = new Uint8Array(n)
    const nodeId = new Int16Array(n).fill(-1)
    const idx = (x: number, y: number) => y * cols + x

    // 1. Sparse background field.
    for (let y = 0; y < rows; y++) {
      for (let x = 0; x < cols; x++) {
        if (rng() < 0.08) {
          chars[idx(x, y)] = pick(rng, TILE_CHARS)
          kind[idx(x, y)] = 1
        }
      }
    }

    // 2. Provider nodes with minimum separation.
    const targetNodes = Math.max(6, Math.min(16, Math.round((cols * rows) / 900)))
    const nodes: MapNode[] = []
    const minDist = Math.max(7, Math.round((cols + rows) / 10))
    let attempts = 0
    while (nodes.length < targetNodes && attempts++ < 400) {
      const nd = {
        x: 2 + Math.floor(rng() * (cols - 4)),
        y: 2 + Math.floor(rng() * (rows - 4)),
        r: 1 + Math.floor(rng() * 2),
      }
      if (nodes.every((o) => Math.hypot(o.x - nd.x, o.y - nd.y) >= minDist)) {
        // Heartbeat: period + phase differ per node so clusters never pulse
        // in sync (spec 2.2).
        nodes.push({
          ...nd,
          periodMs:
            FLOW.heartbeatPeriodMinMs +
            rng() * (FLOW.heartbeatPeriodMaxMs - FLOW.heartbeatPeriodMinMs),
          phase: rng() * Math.PI * 2,
          tiles: [],
        })
      }
    }

    // 3. Routes: Prim MST + extra loop edges, L-shaped runs. The pulse paths
    //    are recorded as tile-index polylines (spec 2.1: pulses follow the
    //    REAL route lines, never random walks).
    const edges = primEdges(nodes)
    const extra = Math.floor(nodes.length / 4)
    for (let e = 0; e < extra; e++) {
      const a = Math.floor(rng() * nodes.length)
      const b = Math.floor(rng() * nodes.length)
      if (a !== b) edges.push([a, b])
    }
    const inCluster = (x: number, y: number) =>
      nodes.some((nd) => Math.abs(x - nd.x) <= nd.r && Math.abs(y - nd.y) <= nd.r)
    const routes: number[][] = []
    for (const [a, b] of edges) {
      const na = nodes[a]
      const nb = nodes[b]
      const path: number[] = [idx(na.x, na.y)]
      const hFirst = rng() < 0.5
      const hChar = pick(rng, H_ROUTE)
      const vChar = pick(rng, V_ROUTE)
      const step = (x: number, y: number, horizontal: boolean) => {
        if (x < 0 || x >= cols || y < 0 || y >= rows) return
        path.push(idx(x, y))
        if (inCluster(x, y)) return
        let ch = horizontal ? hChar : vChar
        if (rng() < 0.12) ch = DOT
        chars[idx(x, y)] = ch
        kind[idx(x, y)] = 2
      }
      const runX = (from: number, to: number, y: number) => {
        const d = Math.sign(to - from)
        for (let x = from + d; d && x !== to + d; x += d) step(x, y, true)
      }
      const runY = (from: number, to: number, x: number) => {
        const d = Math.sign(to - from)
        for (let y = from + d; d && y !== to + d; y += d) step(x, y, false)
      }
      if (hFirst) {
        runX(na.x, nb.x, na.y)
        runY(na.y, nb.y, nb.x)
      } else {
        runY(na.y, nb.y, na.x)
        runX(na.x, nb.x, nb.y)
      }
      // Dedupe consecutive repeats where an L corner coincides with a node.
      const dedup: number[] = []
      for (const p of path) {
        if (dedup.length === 0 || dedup[dedup.length - 1] !== p) dedup.push(p)
      }
      if (dedup.length > 1) routes.push(dedup)
    }

    // 4. Node clusters drawn over routes.
    for (const nd of nodes) {
      for (let dy = -nd.r; dy <= nd.r; dy++) {
        for (let dx = -nd.r; dx <= nd.r; dx++) {
          if (dx * dx + dy * dy > nd.r * nd.r + nd.r) continue
          if (rng() > 0.8) continue
          const px = nd.x + dx
          const py = nd.y + dy
          if (px >= 0 && px < cols && py >= 0 && py < rows) {
            const i = idx(px, py)
            chars[i] = pick(rng, TILE_CHARS)
            kind[i] = 3
            nodeId[i] = nodes.indexOf(nd)
            nd.tiles.push(i)
          }
        }
      }
    }

    // 5. Rare accent chars at a couple of "active" nodes. Kept monochrome on
    //    purpose: colour only appears with activity (spec 2.6).
    const activeCount = Math.min(nodes.length, 2 + (rng() < 0.5 ? 1 : 0))
    for (let i = 0; i < activeCount; i++) {
      const nd = nodes[Math.floor(rng() * nodes.length)]
      if (nd.x >= 0 && nd.x < cols && nd.y >= 0 && nd.y < rows) {
        chars[idx(nd.x, nd.y)] = pick(rng, ACCENT_CHARS)
        kind[idx(nd.x, nd.y)] = 4
      }
      for (let k = 0; k < 2; k++) {
        const ax = nd.x + Math.round((rng() - 0.5) * 2 * nd.r)
        const ay = nd.y + Math.round((rng() - 0.5) * 2 * nd.r)
        if (ax >= 0 && ay >= 0 && ax < cols && ay < rows) {
          chars[idx(ax, ay)] = pick(rng, ACCENT_CHARS)
          kind[idx(ax, ay)] = 4
        }
      }
    }

    // Shuffle candidates + pixel-verify probe tile.
    const shuf: number[] = []
    let probe = 0
    for (let i = 0; i < n; i++) {
      if (kind[i] === 1 || kind[i] === 3) shuf.push(i)
      if (kind[i] !== 0 && chars[i] !== '') probe = i
    }
    return {
      cols,
      rows,
      n,
      chars,
      kind,
      nodeId,
      nodes,
      routes,
      shufflable: Int32Array.from(shuf),
      probeTile: probe,
    }
  }

  // ---- palette: theme tokens read at paint time, cached, rebuilt on theme
  // change (MutationObserver on <html> class) ----
  let pal = {
    speck: 'rgba(236,238,242,0.07)',
    route: 'rgba(236,238,242,0.105)',
    cluster: ['rgba(236,238,242,0.14)'],
    accentTile: 'rgba(236,238,242,0.16)',
    flash: 'rgba(249,115,22,0.5)',
    pulseHead: 'rgba(59,130,246,0.9)',
    pulseTrail: ['rgba(59,130,246,0.45)', 'rgba(59,130,246,0.25)', 'rgba(59,130,246,0.12)'],
    spray: ['rgba(59,130,246,0.9)', 'rgba(59,130,246,0.5)', 'rgba(59,130,246,0.2)'],
    pointer: ['rgba(236,238,242,0.35)', 'rgba(236,238,242,0.18)', 'rgba(236,238,242,0.08)'],
  }

  function token(name: string, fallback: string): string {
    const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
    return v || fallback
  }

  function rgba(hex: string, a: number): string {
    let h = hex.replace('#', '')
    if (h.length === 3) h = h[0] + h[0] + h[1] + h[1] + h[2] + h[2]
    const int = parseInt(h, 16)
    if (Number.isNaN(int)) return `rgba(236,238,242,${a})`
    return `rgba(${(int >> 16) & 255},${(int >> 8) & 255},${int & 255},${a})`
  }

  const HEARTBEAT_STEPS = 16

  function refreshPalette(): void {
    const ink = token('--9x-ink', '#eceef2')
    const accent = token('--9x-accent', '#3b82f6')
    // Light theme has no --9x-orange token; the brand orange is the fallback.
    const orange = token('--9x-orange', '#f97316')
    const b = FLOW.baseOpacity
    const cluster: string[] = []
    for (let s = 0; s < HEARTBEAT_STEPS; s++) {
      const w = 1 + FLOW.heartbeatAmplitude * ((s / (HEARTBEAT_STEPS - 1)) * 2 - 1)
      cluster.push(rgba(ink, Math.max(0.01, b * w)))
    }
    pal = {
      speck: rgba(ink, b * 0.5),
      route: rgba(ink, b * 0.75),
      cluster,
      accentTile: rgba(ink, b * 1.15),
      flash: rgba(orange, 0.5),
      pulseHead: rgba(accent, 0.9),
      pulseTrail: [rgba(accent, 0.45), rgba(accent, 0.25), rgba(accent, 0.12)],
      spray: [rgba(accent, 0.9), rgba(accent, 0.5), rgba(accent, 0.2)],
      pointer: [rgba(ink, 0.35), rgba(ink, 0.18), rgba(ink, 0.08)],
    }
  }

  // ---- canvas state ----
  let canvas: HTMLCanvasElement
  let ctx: CanvasRenderingContext2D | null = null
  let disposed = false
  let world: World | null = null
  let cell = FLOW.cellFallbackPx
  let cssW = 0
  let cssH = 0
  let dpr = 1
  let reduced = false
  let mobileMq: MediaQueryList | null = null
  let reducedMq: MediaQueryList | null = null
  let raf = 0
  let lastRender = 0
  let nowMs = 0
  let paintedOnce = false

  // Pools — allocated once, reused forever (no hot-loop allocation).
  const MAX_PULSES = FLOW.pulseCountDesktop + 2
  const pulses: Pulse[] = Array.from({ length: MAX_PULSES }, () => ({
    active: false,
    burst: false,
    path: null,
    pos: 0,
    dir: 1,
    tilesPerSec: 10,
    wf: 1,
    wp: 0,
  }))
  const FX_CAP = 1024
  let fxPrev = new Int32Array(FX_CAP)
  let fxCur = new Int32Array(FX_CAP)
  let fxPrevLen = 0
  let fxCurLen = 0
  const DIRTY_CAP = 4096
  let dirtyList = new Int32Array(DIRTY_CAP)
  let dirtyCount = 0
  let stamp = new Uint32Array(1)
  let frameStamp = 0
  let fullRedraw = false
  let shuffleDebt = 0

  interface PointerPt {
    x: number
    y: number
    born: number
  }
  const pointerPts: PointerPt[] = Array.from({ length: FLOW.pointerPool }, () => ({
    x: 0,
    y: 0,
    born: -1e9,
  }))
  let ptrCursor = 0
  let ptrLastX = -99
  let ptrLastY = -99

  interface BurstFx {
    active: boolean
    born: number
    ch: string[]
    x: Float32Array
    y: Float32Array
    vx: Float32Array
    vy: Float32Array
  }
  const bursts: BurstFx[] = Array.from({ length: 4 }, () => ({
    active: false,
    born: 0,
    ch: new Array<string>(FLOW.burstSprayTiles).fill('>'),
    x: new Float32Array(FLOW.burstSprayTiles),
    y: new Float32Array(FLOW.burstSprayTiles),
    vx: new Float32Array(FLOW.burstSprayTiles),
    vy: new Float32Array(FLOW.burstSprayTiles),
  }))
  const SPRAY_CHARS = ['>', '#', '+']
  interface RippleFx {
    active: boolean
    cx: number
    cy: number
    born: number
  }
  const ripples: RippleFx[] = Array.from({ length: 4 }, () => ({
    active: false,
    cx: 0,
    cy: 0,
    born: 0,
  }))

  // Per-node dynamic state (sized to world on regen).
  let nidStep = new Int8Array(1)
  let nidFlashUntil = new Float64Array(1)
  let nidHadFlash = new Uint8Array(1)

  // ---- geometry helpers ----
  let rectCache = { left: 0, top: 0 }
  function refreshRect(): void {
    if (!canvas) return
    const r = canvas.getBoundingClientRect()
    rectCache.left = r.left
    rectCache.top = r.top
  }
  function toTileF(clientX: number, clientY: number): { x: number; y: number } {
    return { x: (clientX - rectCache.left) / cell, y: (clientY - rectCache.top) / cell }
  }

  function setFont(): void {
    if (!ctx) return
    const family = getComputedStyle(canvas).fontFamily || 'monospace'
    ctx.font = `${cell}px ${family}`
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
  }

  // ---- drawing ----
  function drawBase(i: number): void {
    const w = world
    const c = ctx
    if (!c || !w) return
    const k = w.kind[i]
    if (k === 0) return
    const x = (i % w.cols) * cell
    const y = ((i / w.cols) | 0) * cell
    if (k === 3) {
      const nid = w.nodeId[i]
      if (nid >= 0 && nidFlashUntil[nid] > nowMs) c.fillStyle = pal.flash
      else if (nid >= 0) c.fillStyle = pal.cluster[nidStep[nid]]
      else c.fillStyle = pal.speck
    } else if (k === 2) c.fillStyle = pal.route
    else if (k === 4) c.fillStyle = pal.accentTile
    else c.fillStyle = pal.speck
    c.fillText(w.chars[i], x + cell / 2, y + cell / 2)
  }

  function clearTile(i: number): void {
    const w = world
    const c = ctx
    if (!c || !w) return
    c.clearRect((i % w.cols) * cell, ((i / w.cols) | 0) * cell, cell, cell)
  }

  function fullDraw(): void {
    const w = world
    const c = ctx
    if (!c || !w) return
    c.clearRect(0, 0, cssW, cssH)
    for (let i = 0; i < w.n; i++) drawBase(i)
    fxPrevLen = 0
    fxCurLen = 0
  }

  function markDirty(i: number): void {
    if (fullRedraw || i < 0 || i >= (world?.n ?? 0)) return
    if (stamp[i] === frameStamp) return
    stamp[i] = frameStamp
    if (dirtyCount >= DIRTY_CAP) {
      fullRedraw = true // degrade gracefully instead of dropping tiles
      return
    }
    dirtyList[dirtyCount++] = i
  }

  function pushFx(i: number): void {
    if (fxCurLen < FX_CAP) fxCur[fxCurLen++] = i
  }

  // ---- setup / first paint ----
  function regenWorld(w: number, h: number): void {
    cssW = w
    cssH = h
    dpr = Math.min(window.devicePixelRatio || 1, FLOW.dprCap)
    canvas.width = Math.max(1, Math.round(w * dpr))
    canvas.height = Math.max(1, Math.round(h * dpr))
    ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    const cellTok = parseFloat(token('--9x-cell', ''))
    cell = Number.isFinite(cellTok) && cellTok > 4 ? cellTok : FLOW.cellFallbackPx
    setFont()
    const cols = Math.max(4, Math.ceil(w / cell))
    const rows = Math.max(4, Math.ceil(h / cell))
    world = generateWorld(cols, rows)
    stamp = new Uint32Array(world.n)
    nidStep = new Int8Array(world.nodes.length)
    nidFlashUntil = new Float64Array(world.nodes.length)
    nidHadFlash = new Uint8Array(world.nodes.length)
    fullDraw()
    paintedOnce = true
  }

  /** Pixel-level proof that the canvas actually painted (first-paint race). */
  function verifyPainted(): boolean {
    const w = world
    const c = ctx
    if (!c || !w) return false
    if (canvas.width < 4 || canvas.height < 4) return false
    const i = w.probeTile
    const d = Math.max(1, Math.round(cell * dpr))
    const dx = Math.min(canvas.width - d, Math.round((i % w.cols) * cell * dpr))
    const dy = Math.min(canvas.height - d, Math.round(((i / w.cols) | 0) * cell * dpr))
    try {
      const data = c.getImageData(dx, dy, d, d).data
      for (let p = 3; p < data.length; p += 4) if (data[p] > 0) return true
    } catch {
      return true // tainted canvas cannot be probed; trust the draw call
    }
    return w.kind[i] === 0
  }

  function attemptPaint(): boolean {
    if (disposed) return true
    const r = canvas.getBoundingClientRect()
    if (r.width >= 8 && r.height >= 8) {
      regenWorld(r.width, r.height)
      refreshRect()
      if (verifyPainted()) {
        startLoop() // late first paint (retry/RO path) arms the animation
        return true
      }
      return false
    }
    return false
  }

  // ---- pulses ----
  function respawn(p: Pulse, fast: boolean): void {
    const w = world
    if (!w || w.routes.length === 0) {
      p.active = false
      return
    }
    let path: number[]
    if (fast && w.routes.length > 1) {
      // Burst pulse: nearest route to the burst origin (small one-off scan;
      // the origin rides in p.pos/p.wp and is overwritten below).
      let best = 0
      let bestD = Infinity
      for (let r = 0; r < w.routes.length; r++) {
        const mid = w.routes[r][w.routes[r].length >> 1]
        const mx = (mid % w.cols) - p.pos
        const my = ((mid / w.cols) | 0) - p.wp
        const d = mx * mx + my * my
        if (d < bestD) {
          bestD = d
          best = r
        }
      }
      path = w.routes[best]
    } else {
      path = w.routes[(Math.random() * w.routes.length) | 0]
    }
    p.path = path
    p.dir = Math.random() < 0.5 ? 1 : -1
    p.pos = p.dir === 1 ? 0 : path.length - 1
    const dur = fast
      ? FLOW.pulseMinDurationMs * 0.45
      : FLOW.pulseMinDurationMs + Math.random() * (FLOW.pulseMaxDurationMs - FLOW.pulseMinDurationMs)
    p.tilesPerSec = (path.length - 1) / (dur / 1000)
    p.wf = 0.5 + Math.random() * 1.2
    p.wp = Math.random() * Math.PI * 2
    p.active = true
  }

  // ---- frame ----
  function frame(ts: number): void {
    if (disposed || reduced || document.hidden) {
      raf = 0 // loop stays cancelled while hidden/reduced; resume re-arms it
      return
    }
    raf = requestAnimationFrame(frame)
    const fps = mobileMq?.matches ? FLOW.mobileFpsCap : 60
    if (ts - lastRender < 1000 / fps - 2) return // mobile 30fps throttle
    const dt = Math.min(100, lastRender ? ts - lastRender : 16)
    lastRender = ts
    nowMs = ts
    renderFrame(dt)
  }

  function renderFrame(dt: number): void {
    const w = world
    const c = ctx
    if (!c || !w) return
    frameStamp = (frameStamp + 1) >>> 0 // matches the Uint32Array stamp store
    dirtyCount = 0
    fullRedraw = false
    fxCurLen = 0

    // Restore last frame's effect tiles to their base appearance first.
    for (let k = 0; k < fxPrevLen; k++) markDirty(fxPrev[k])

    // -- 2.1 packet pulses + 2.2 node flash --
    const isMobile = mobileMq?.matches === true
    const target = isMobile ? FLOW.pulseCountMobile : FLOW.pulseCountDesktop
    let running = 0
    for (let k = 0; k < MAX_PULSES; k++) {
      const p = pulses[k]
      if (p.active && !p.burst) running++
    }
    for (let k = 0; k < MAX_PULSES; k++) {
      const p = pulses[k]
      if (!p.active) {
        if (!p.burst && running < target) {
          respawn(p, false)
          if (p.active) running++
        }
        continue
      }
      const path = p.path as number[]
      const mod = 0.75 + 0.25 * Math.sin(nowMs * 0.001 * p.wf + p.wp) // speed easing
      p.pos += p.dir * p.tilesPerSec * mod * (dt / 1000)
      if (p.pos <= 0 || p.pos >= path.length - 1) {
        if (p.burst) {
          p.active = false
          continue
        }
        respawn(p, false)
        continue
      }
      const headIdx = path[Math.floor(p.pos)]
      const nid = w.nodeId[headIdx]
      if (nid >= 0) {
        nidFlashUntil[nid] = nowMs + FLOW.nodeFlashMs
      }
      pushFx(headIdx)
      markDirty(headIdx)
      for (let t = 1; t <= FLOW.trailTiles; t++) {
        const ti = Math.floor(p.pos) - t * p.dir
        if (ti < 0 || ti >= path.length) break
        pushFx(path[ti])
        markDirty(path[ti])
      }
    }

    // -- 2.2 node heartbeat (quantised brightness, redraw only on step change) --
    for (let nid = 0; nid < w.nodes.length; nid++) {
      const nd = w.nodes[nid]
      const s = Math.sin((nowMs / nd.periodMs) * Math.PI * 2 + nd.phase)
      const step = Math.min(
        HEARTBEAT_STEPS - 1,
        Math.max(0, Math.round(((s + 1) / 2) * (HEARTBEAT_STEPS - 1))),
      )
      const flash = nidFlashUntil[nid] > nowMs ? 1 : 0
      if (step !== nidStep[nid] || flash !== nidHadFlash[nid]) {
        nidStep[nid] = step
        nidHadFlash[nid] = flash
        const tiles = nd.tiles
        for (let t = 0; t < tiles.length; t++) markDirty(tiles[t])
      }
    }

    // -- 2.3 glyph shuffle (instant char swap; no persistent state) --
    const rate = isMobile ? FLOW.shuffleRateMobilePerSec : FLOW.shuffleRateDesktopPerSec
    shuffleDebt += rate * (dt / 1000)
    while (shuffleDebt >= 1) {
      shuffleDebt -= 1
      if (w.shufflable.length === 0) break
      const i = w.shufflable[(Math.random() * w.shufflable.length) | 0]
      w.chars[i] = TILE_CHARS[(Math.random() * TILE_CHARS.length) | 0]
      markDirty(i)
    }

    // -- 2.5.1 pointer sonar trail --
    for (let k = 0; k < FLOW.pointerPool; k++) {
      const pt = pointerPts[k]
      const age = nowMs - pt.born
      if (age < 0 || age >= FLOW.pointerFadeMs) continue
      pointerTiles(pt, age, true)
    }

    // -- 2.5.2 action bursts --
    for (let k = 0; k < bursts.length; k++) {
      const b = bursts[k]
      if (!b.active) continue
      const age = nowMs - b.born
      if (age >= FLOW.burstLifeMs) {
        b.active = false
        continue
      }
      const ageSec = age / 1000
      for (let s = 0; s < FLOW.burstSprayTiles; s++) {
        const tx = Math.round(b.x[s] + b.vx[s] * ageSec)
        const ty = Math.round(b.y[s] + b.vy[s] * ageSec)
        if (tx < 0 || ty < 0 || tx >= w.cols || ty >= w.rows) continue
        const i = ty * w.cols + tx
        pushFx(i)
        markDirty(i)
      }
    }

    // -- 2.5.3 tap ripple --
    for (let k = 0; k < ripples.length; k++) {
      const rp = ripples[k]
      if (!rp.active) continue
      const age = nowMs - rp.born
      if (age >= FLOW.rippleLifeMs) {
        rp.active = false
        continue
      }
      rippleTiles(rp, age, true)
    }

    // -- draw: dirty tiles only (clear + repaint base; the CSS gridline
    //    underlay shows through clearRect for free) --
    if (fullRedraw) {
      fullDraw()
    } else {
      for (let k = 0; k < dirtyCount; k++) {
        const i = dirtyList[k]
        clearTile(i)
        drawBase(i)
      }
    }

    // -- overlays (on top of the restored base tiles) --
    for (let k = 0; k < MAX_PULSES; k++) {
      const p = pulses[k]
      if (!p.active) continue
      const path = p.path as number[]
      drawPulseTile(path[Math.floor(p.pos)], pal.pulseHead)
      for (let t = 1; t <= FLOW.trailTiles; t++) {
        const ti = Math.floor(p.pos) - t * p.dir
        if (ti < 0 || ti >= path.length) break
        drawPulseTile(path[ti], pal.pulseTrail[t - 1])
      }
    }
    for (let k = 0; k < FLOW.pointerPool; k++) {
      const pt = pointerPts[k]
      const age = nowMs - pt.born
      if (age < 0 || age >= FLOW.pointerFadeMs) continue
      pointerTiles(pt, age, false)
    }
    for (let k = 0; k < bursts.length; k++) {
      const b = bursts[k]
      if (!b.active) continue
      const age = nowMs - b.born
      const fade = 1 - age / FLOW.burstLifeMs
      c.fillStyle = pal.spray[fade > 0.6 ? 0 : fade > 0.3 ? 1 : 2]
      const ageSec = age / 1000
      for (let s = 0; s < FLOW.burstSprayTiles; s++) {
        const tx = Math.round(b.x[s] + b.vx[s] * ageSec)
        const ty = Math.round(b.y[s] + b.vy[s] * ageSec)
        if (tx < 0 || ty < 0 || tx >= w.cols || ty >= w.rows) continue
        c.fillText(b.ch[s], tx * cell + cell / 2, ty * cell + cell / 2)
      }
    }
    for (let k = 0; k < ripples.length; k++) {
      const rp = ripples[k]
      if (!rp.active) continue
      rippleTiles(rp, nowMs - rp.born, false)
    }

    // Swap fx buffers: this frame's effect tiles become next frame's restore set.
    const tmp = fxPrev
    fxPrev = fxCur
    fxCur = tmp
    fxPrevLen = fxCurLen
  }

  /** Shared by the dirty-marking pass and the overlay pass. */
  function pointerTiles(pt: PointerPt, age: number, markOnly: boolean): void {
    const w = world
    const c = ctx
    if (!w || !c) return
    const fade = 1 - age / FLOW.pointerFadeMs
    if (!markOnly) c.fillStyle = pal.pointer[fade > 0.6 ? 0 : fade > 0.3 ? 1 : 2]
    const cx = Math.round(pt.x)
    const cy = Math.round(pt.y)
    for (let dy = -1; dy <= 1; dy++) {
      for (let dx = -1; dx <= 1; dx++) {
        if (dx !== 0 && dy !== 0) continue // plus shape, 5 tiles
        const tx = cx + dx
        const ty = cy + dy
        if (tx < 0 || ty < 0 || tx >= w.cols || ty >= w.rows) continue
        const i = ty * w.cols + tx
        if (markOnly) {
          pushFx(i)
          markDirty(i)
        } else {
          c.fillRect(tx * cell + 1, ty * cell + 1, cell - 2, cell - 2)
        }
      }
    }
  }

  /** Shared by the dirty-marking pass and the overlay pass. */
  function rippleTiles(rp: RippleFx, age: number, markOnly: boolean): void {
    const w = world
    const c = ctx
    if (!w || !c) return
    const fade = 1 - age / FLOW.rippleLifeMs
    for (let ring = 0; ring < FLOW.rippleRings; ring++) {
      const rr = (age / FLOW.rippleLifeMs) * FLOW.rippleMaxRadius + ring * 0.9
      if (!markOnly) {
        c.fillStyle = pal.pointer[ring === 0 ? 0 : ring === 1 ? 1 : 2]
        c.globalAlpha = Math.max(0, fade)
      }
      const samples = 18
      for (let s = 0; s < samples; s++) {
        const a = (s / samples) * Math.PI * 2
        const tx = Math.round(rp.cx + Math.cos(a) * rr)
        const ty = Math.round(rp.cy + Math.sin(a) * rr)
        if (tx < 0 || ty < 0 || tx >= w.cols || ty >= w.rows) continue
        const i = ty * w.cols + tx
        if (markOnly) {
          pushFx(i)
          markDirty(i)
        } else {
          c.fillRect(tx * cell + 1, ty * cell + 1, cell - 2, cell - 2)
        }
      }
      if (!markOnly) c.globalAlpha = 1
    }
  }

  function drawPulseTile(i: number, color: string): void {
    const w = world
    const c = ctx
    if (!c || !w) return
    c.fillStyle = color
    c.fillRect((i % w.cols) * cell + 1, ((i / w.cols) | 0) * cell + 1, cell - 2, cell - 2)
  }

  // ---- interaction (2.5) ----
  function onPointerMove(e: PointerEvent): void {
    if (reduced || !world) return
    recordPointer(e.clientX, e.clientY)
  }
  function onTouchMove(e: TouchEvent): void {
    if (reduced || !world || e.touches.length === 0) return
    recordPointer(e.touches[0].clientX, e.touches[0].clientY)
  }
  function recordPointer(cx: number, cy: number): void {
    const w = world
    if (!w) return
    const p = toTileF(cx, cy)
    if (p.x < 0 || p.y < 0 || p.x >= w.cols || p.y >= w.rows) return
    // Spatial sampling: only light a new point when the cursor moved ~1 tile.
    if (Math.abs(p.x - ptrLastX) < 1 && Math.abs(p.y - ptrLastY) < 1) return
    ptrLastX = p.x
    ptrLastY = p.y
    const slot = pointerPts[ptrCursor % FLOW.pointerPool]
    ptrCursor++
    slot.x = p.x
    slot.y = p.y
    slot.born = performance.now()
  }

  // Click exclusions: anything interactive or panel-like never ripples.
  const BG_EXCLUDE =
    'button, a, input, select, textarea, label, summary, nav, header, footer, aside, form, table, dialog, ' +
    '[role="button"], [role="menuitem"], [role="tab"], [role="switch"], [role="checkbox"], ' +
    '.glyph-surface, .glyph-toast, .gl-panel, [data-bg-ignore]'
  function onClick(e: MouseEvent): void {
    if (reduced || !world) return
    const t = e.target
    if (!t || typeof (t as Element).closest !== 'function') return
    if ((t as Element).closest(BG_EXCLUDE)) return
    const p = toTileF(e.clientX, e.clientY)
    spawnRipple(p.x, p.y)
  }

  function effectsActive(): number {
    let c = 0
    for (const b of bursts) if (b.active) c++
    for (const r of ripples) if (r.active) c++
    return c
  }

  function spawnRipple(cx: number, cy: number): void {
    if (!world) return
    // Spec 2.5.5 caps simultaneous effects at 3: recycle the oldest slot.
    if (effectsActive() >= FLOW.maxEffects) {
      let oldest = ripples[0]
      for (const r of ripples) if (r.born < oldest.born) oldest = r
      oldest.active = false
    }
    for (const r of ripples) {
      if (!r.active) {
        r.active = true
        r.cx = cx
        r.cy = cy
        r.born = performance.now()
        return
      }
    }
  }

  function spawnBurst(x?: number, y?: number): void {
    const w = world
    if (!w || reduced) return
    if (effectsActive() >= FLOW.maxEffects) {
      let oldest = bursts[0]
      for (const b of bursts) if (b.active && b.born < oldest.born) oldest = b
      oldest.active = false
    }
    let ox: number
    let oy: number
    if (typeof x === 'number' && typeof y === 'number') {
      const p = toTileF(x, y)
      ox = Math.max(0, Math.min(w.cols - 1, p.x))
      oy = Math.max(0, Math.min(w.rows - 1, p.y))
    } else if (w.routes.length > 0) {
      // No action position given (e.g. a toast): burst from a random route.
      const i = w.routes[(Math.random() * w.routes.length) | 0][0]
      ox = i % w.cols
      oy = (i / w.cols) | 0
    } else {
      ox = w.cols / 2
      oy = w.rows / 2
    }
    for (const b of bursts) {
      if (b.active) continue
      b.active = true
      b.born = performance.now()
      for (let k = 0; k < FLOW.burstSprayTiles; k++) {
        const a = Math.random() * Math.PI * 2
        const sp = 2 + Math.random() * 3 // tiles/sec
        b.ch[k] = SPRAY_CHARS[(Math.random() * SPRAY_CHARS.length) | 0]
        b.x[k] = ox
        b.y[k] = oy
        b.vx[k] = Math.cos(a) * sp
        b.vy[k] = Math.sin(a) * sp
      }
      break
    }
    // 1 fast pulse along the nearest route (origin rides in p.pos/p.wp into
    // the respawn() route scan, which then overwrites both fields).
    for (const p of pulses) {
      if (p.active) continue
      p.burst = true
      p.pos = ox
      p.wp = oy
      respawn(p, true)
      break
    }
  }

  // ---- loop control ----
  function startLoop(): void {
    if (disposed || raf !== 0 || reduced || !paintedOnce) return
    lastRender = 0
    raf = requestAnimationFrame(frame)
  }
  function stopLoop(): void {
    if (raf !== 0) {
      cancelAnimationFrame(raf)
      raf = 0
    }
  }

  onMount(() => {
    if (!canvas) return
    ctx = canvas.getContext('2d')
    reducedMq = window.matchMedia('(prefers-reduced-motion: reduce)')
    mobileMq = window.matchMedia('(max-width: 767px)')
    reduced = reducedMq.matches
    refreshPalette()
    refreshRect()

    // First paint: synchronous attempt + double-RAF + verify-retry loop.
    // See the header comment for why all four paths exist.
    let tries = 0
    let retryTimer = 0
    let raf2 = 0
    attemptPaint()
    const retry = () => {
      if (disposed || paintedOnce) return
      tries++
      if (attemptPaint()) return
      retryTimer = window.setTimeout(() => {
        if (!disposed) raf2 = requestAnimationFrame(retry)
      }, Math.min(50 * tries, 250))
    }
    requestAnimationFrame(() => {
      requestAnimationFrame(retry)
    })

    // Resize: ResizeObserver re-runs the same verified paint path.
    let resizeTimer = 0
    const ro = new ResizeObserver(() => {
      if (disposed) return
      window.clearTimeout(resizeTimer)
      resizeTimer = window.setTimeout(() => {
        attemptPaint()
      }, 120)
    })
    ro.observe(canvas)

    // Fonts: glyph metrics change once Geist Mono lands; repaint once.
    if (document.fonts?.ready) {
      document.fonts.ready.then(() => {
        if (disposed) return
        setFont()
        fullDraw()
      })
    }

    // Theme: the app toggles classes on <html> (dark/light/glyph-light).
    const themeObs = new MutationObserver(() => {
      if (disposed) return
      refreshPalette()
      fullDraw() // colour cache is rebuilt above; repaint everything once
    })
    themeObs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })

    // Reduced motion can flip at runtime: static map only when it is on.
    const onReducedChange = () => {
      reduced = reducedMq?.matches ?? false
      stopLoop()
      if (!reduced) startLoop()
      else fullDraw()
    }
    reducedMq.addEventListener('change', onReducedChange)

    // Battery guard: hidden tab cancels the loop entirely (spec §3).
    const onVis = () => {
      if (document.hidden) stopLoop()
      else startLoop()
    }
    document.addEventListener('visibilitychange', onVis)

    window.addEventListener('pointermove', onPointerMove, { passive: true })
    window.addEventListener('touchmove', onTouchMove, { passive: true })
    window.addEventListener('scroll', refreshRect, { passive: true })
    document.addEventListener('click', onClick, true)
    const offBurst = onRoutingBurst((x, y) => spawnBurst(x, y))

    startLoop()

    return () => {
      disposed = true
      stopLoop()
      ro.disconnect()
      themeObs.disconnect()
      reducedMq?.removeEventListener('change', onReducedChange)
      document.removeEventListener('visibilitychange', onVis)
      window.removeEventListener('pointermove', onPointerMove)
      window.removeEventListener('touchmove', onTouchMove)
      window.removeEventListener('scroll', refreshRect)
      document.removeEventListener('click', onClick, true)
      offBurst()
      window.clearTimeout(retryTimer)
      window.clearTimeout(resizeTimer)
      if (raf2) cancelAnimationFrame(raf2)
    }
  })
</script>

<canvas bind:this={canvas} aria-hidden="true"></canvas>

<style>
  canvas {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    display: block;
    pointer-events: none;
    user-select: none;
    font-family: var(--font-code, 'Geist Mono', 'JetBrains Mono', ui-monospace, monospace);
    /* Faint graph-paper gridline underlay: CSS, so canvas clearRect reveals
       it for free during dirty-rect redraws. */
    background-image:
      linear-gradient(to right, var(--9x-gridline, rgba(236, 238, 242, 0.035)) 1px, transparent 1px),
      linear-gradient(to bottom, var(--9x-gridline, rgba(236, 238, 242, 0.035)) 1px, transparent 1px);
    background-size: var(--9x-cell, 12px) var(--9x-cell, 12px);
  }
</style>
