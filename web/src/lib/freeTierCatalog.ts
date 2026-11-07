// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
/**
 * The free-tier catalog: what the well-known providers give away, and under
 * which terms, ported from OmniRoute's freeModelCatalog concept.
 *
 * The house rule the catalog inherits: no evidence, no number. Every row
 * carries an `evidence:` comment naming the class the figure came from:
 *   - public-page      — the provider's own published pricing or rate-limit page
 *   - api-public       — a value the provider's API answers with (headers, endpoints)
 *   - console-verified — read off the provider console or this repo's own gating
 * A row whose cap is unpublished carries 0 and says so in its notes; the page
 * renders that as "unpublished" and the headline ignores it.
 *
 * Token figures are ESTIMATES converted from the provider's own published caps
 * at TOKENS_PER_REQUEST_ASSUMPTION below, or the provider's own token-denominated
 * cap where it publishes one. They describe a steady month of full use, not a
 * guarantee; the footer says so and the curation date bounds the claim.
 */

export type FreeTierRegime = 'recurring-daily' | 'recurring-monthly' | 'recurring-credit' | 'keyless'

export type FreeTierTos = 'ok' | 'caution' | 'avoid'

export interface FreeTierRow {
  /** Provider as the dashboard names it. */
  provider: string
  /**
   * Rows that draw from one capacity pool share a poolKey. The headline sums
   * recurring rows pool-deduped: a pool counts once, at its largest member,
   * because stacking two halves of one wallet double-counts the same tokens.
   * Today each row owns its own pool; the field exists so the dedup is already
   * in place when a row is split into conditions.
   */
  poolKey: string
  /**
   * The gateway provider id the tier routes through — the key
   * GET /api/free-tier/live books usage under. Absent for a tier the gateway
   * does not route (the keyless web sessions), which is why those rows never
   * show a live counter.
   */
  providerId?: string
  /** The models the tier covers, as the provider names them. */
  models: string[]
  /** Estimated steady-monthly tokens. 0 = the provider publishes no token cap. */
  monthlyTokens: number
  freeType: FreeTierRegime
  tos: FreeTierTos
  notes: string
}

/** Average tokens per request the estimates assume; stated so it can be argued with. */
export const TOKENS_PER_REQUEST_ASSUMPTION = 4_000

/** The day the figures were last read off their sources. */
export const CATALOG_CURATED_AT = '2026-11-06'

const DAY = 30 // steady-month convention: 30 days of the published daily cap

export const FREE_TIER_CATALOG: FreeTierRow[] = [
  {
    provider: 'Google AI Studio',
    poolKey: 'google-ai-studio',
    providerId: 'gemini',
    models: ['gemini-2.5-flash', 'gemini-2.5-flash-lite'],
    monthlyTokens: 250 * DAY * TOKENS_PER_REQUEST_ASSUMPTION,
    freeType: 'recurring-daily',
    tos: 'ok',
    notes: 'Free tier: 250 req/day on 2.5 Flash (1,000 on Flash-Lite), 10-15 RPM. Per-project per-model caps.',
    // evidence: public-page — ai.google.dev/gemini-api/docs/rate-limits
  },
  {
    provider: 'Groq',
    poolKey: 'groq',
    providerId: 'groq',
    models: ['llama-3.3-70b-versatile', 'llama-3.1-8b-instant'],
    monthlyTokens: 1_000 * DAY * TOKENS_PER_REQUEST_ASSUMPTION,
    freeType: 'recurring-daily',
    tos: 'ok',
    notes: 'Free tier: ~1,000 req/day on 70B-class models, ~14,400 req/day on 8B-class. Prompt/candidate caching included.',
    // evidence: public-page — console.groq.com/docs/rate-limits
  },
  {
    provider: 'Cerebras',
    poolKey: 'cerebras',
    providerId: 'cerebras',
    models: ['llama-3.3-70b', 'qwen-3-32b'],
    monthlyTokens: 1_000_000 * DAY,
    freeType: 'recurring-daily',
    tos: 'ok',
    notes: 'Free tier publishes a token-denominated cap: ~1M tokens/day at 30 RPM / 60k TPM.',
    // evidence: public-page — cerebras.ai inference pricing/rate-limit docs
  },
  {
    provider: 'Mistral La Plateforme',
    poolKey: 'mistral',
    providerId: 'mistral',
    models: ['mistral-small-latest', 'codestral-latest'],
    monthlyTokens: 1_000_000_000,
    freeType: 'recurring-monthly',
    tos: 'caution',
    notes: 'Experiment plan: 1B tokens/month at 4 RPM. Free tier may train on your data unless opted out.',
    // evidence: public-page — docs.mistral.ai deployment types/tier table
  },
  {
    provider: 'OpenRouter :free',
    poolKey: 'openrouter-free',
    providerId: 'openrouter',
    models: ['deepseek/deepseek-r1:free', 'meta-llama/llama-3.3-70b-instruct:free'],
    monthlyTokens: 50 * DAY * TOKENS_PER_REQUEST_ASSUMPTION,
    freeType: 'recurring-daily',
    tos: 'caution',
    notes: '50 req/day below $10 lifetime credits (1,000 req/day once crossed). Publishers of :free variants may train on prompts.',
    // evidence: public-page — openrouter.ai/docs/api-reference/limits
  },
  {
    provider: 'GitHub Models',
    poolKey: 'github-models',
    providerId: 'github',
    models: ['gpt-4.1-mini', 'llama-3.3-70b', 'phi-4'],
    monthlyTokens: 150 * DAY * TOKENS_PER_REQUEST_ASSUMPTION,
    freeType: 'recurring-daily',
    tos: 'caution',
    notes: 'Catalog free with a GitHub PAT: ~150 req/day on high-tier models at 15 RPM. TOS marks it non-production; inputs feed model testing.',
    // evidence: public-page — docs.github.com/github-models rate-limit table
  },
  {
    provider: 'Cloudflare Workers AI',
    poolKey: 'cloudflare-workers-ai',
    providerId: 'cloudflare-ai',
    models: ['llama-3.2-3b', 'flux-1-schnell'],
    monthlyTokens: 80_000 * DAY,
    freeType: 'recurring-daily',
    tos: 'ok',
    notes: '10,000 neurons/day free. Neuron cost varies per model; figure assumes Llama-class text generation (~80k tokens/day equivalent).',
    // evidence: public-page — developers.cloudflare.com/workers-ai/platform/pricing
  },
  {
    provider: 'NVIDIA NIM',
    poolKey: 'nvidia-nim',
    providerId: 'nvidia',
    models: ['open AI catalog at build.nvidia.com'],
    monthlyTokens: 1_000 * TOKENS_PER_REQUEST_ASSUMPTION,
    freeType: 'recurring-credit',
    tos: 'ok',
    notes: '1,000 API credits per personal account (1 credit = 1 call), valid 18 months. One-off, not monthly.',
    // evidence: public-page — build.nvidia.com terms/credits
  },
  {
    provider: 'HuggingFace Inference Providers',
    poolKey: 'hf-inference-providers',
    providerId: 'huggingface',
    models: ['routed to partner providers (Groq, Cerebras, ...)'],
    monthlyTokens: 500_000,
    freeType: 'recurring-credit',
    tos: 'caution',
    notes: 'Free accounts carry a small monthly inference credit (~$0.10); PRO raises it. Routed spend draws the partner pool too.',
    // evidence: public-page — huggingface.co/docs/inference-providers pricing
  },
  {
    provider: 'Together AI',
    poolKey: 'together',
    providerId: 'together',
    models: ['llama-3.3-70b (free variant lineup)'],
    monthlyTokens: 2_000_000,
    freeType: 'recurring-credit',
    tos: 'caution',
    notes: 'Free-variant models at tight RPM plus a signup credit; the free lineup rotates without notice.',
    // evidence: public-page — docs.together.ai free-model rate limits
  },
  {
    provider: 'Z.AI (GLM-4-Flash)',
    poolKey: 'zai-glm-flash',
    providerId: 'glm',
    models: ['glm-4-flash'],
    monthlyTokens: 50_000_000,
    freeType: 'recurring-monthly',
    tos: 'ok',
    notes: 'GLM-4-Flash is free on the open platform, concurrency-limited with no published token cap; estimate assumes a busy single key.',
    // evidence: public-page — open.bigmodel.cn pricing (flash listed free)
  },
  {
    provider: 'Consumer web sessions',
    poolKey: 'consumer-web-sessions',
    models: ['gemini web', 'chatgpt web', 'deepseek web'],
    monthlyTokens: 0,
    freeType: 'keyless',
    tos: 'avoid',
    notes: 'Cookie-session automation of consumer chat products. No published cap to cite, and automating the consumer UI sits outside provider TOS. 9RouterX ships these executors OFF.',
    // evidence: console-verified — this repo gates web executors behind ENABLE_WEB_EXECUTORS (executor/web_gate.go), off by default
  },
]

/**
 * The headline figure: the token sum a steady month of full use could reach.
 * Only recurring regimes count (a credit is one-off, a keyless session has no
 * citable cap), and pools count once at their largest member.
 */
export function combinedSteadyMonthlyTokens(rows: FreeTierRow[] = FREE_TIER_CATALOG): number {
  const byPool = new Map<string, number>()
  for (const row of rows) {
    if (row.freeType !== 'recurring-daily' && row.freeType !== 'recurring-monthly') continue
    const current = byPool.get(row.poolKey) ?? 0
    byPool.set(row.poolKey, Math.max(current, row.monthlyTokens))
  }
  let total = 0
  for (const value of byPool.values()) total += value
  return total
}

/** "1.2B", "340M", "800K" — the same shape the usage charts print. */
export function formatTokenEstimate(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`
  return String(n)
}

export const REGIME_LABELS: Record<FreeTierRegime, string> = {
  'recurring-daily': 'daily reset',
  'recurring-monthly': 'monthly reset',
  'recurring-credit': 'one-off credit',
  keyless: 'no key',
}

export const TOS_LABELS: Record<FreeTierTos, string> = {
  ok: 'TOS clear',
  caution: 'read TOS',
  avoid: 'TOS risk',
}
