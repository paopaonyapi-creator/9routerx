// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package providers

import (
	"sort"
	"strings"
	"sync"
)

// Free-model detection, ported from OmniRoute's
// `src/shared/utils/freeModels.ts` + `open-sse/config/freeModelCatalog.*`.
//
// The dashboard shows a green "Free" badge next to a model, so the two have to
// answer "is this free?" the same way. Two regimes decide that, on purpose,
// exactly as upstream does:
//
//   - A **provider** is considered to have a free tier when it appears in the
//     catalogued free-provider list below.
//   - A **model** is free when its id is listed in the shipped catalogue for
//     that provider, or — only for a provider with a documented free tier — it
//     carries a payload free signal: an `isFree: true` flag, an OpenRouter
//     style `:free` id suffix, or zero prompt *and* completion prices.
//
// A regime may retire a free tier behind a paid key (`discontinued`); such
// entries are filtered out at build time (see `freeBudgetGrantsAccess`) so a
// discontinued id is never reported free.
//
// The catalogue now covers every routed provider with a documented free tier,
// synced from OmniRoute's `open-sse/config/freeModelCatalog.data.ts`
// (FREE_CATALOG_CURATED_AT 2026-10-30; sync 2026-11-08). A provider added to
// the registry without an entry here is still safe — it simply never shows a
// Free badge, which is the conservative default.

// freeRegime is the documented free-tier regime of a catalogue entry.
type freeRegime string

const (
	regimeRecurringDaily    freeRegime = "recurring-daily"
	regimeRecurringMonthly  freeRegime = "recurring-monthly"
	regimeRecurringCredit   freeRegime = "recurring-credit"
	regimeRecurringUncapped freeRegime = "recurring-uncapped"
	regimeOneTimeInitial    freeRegime = "one-time-initial"
	regimeKeyless           freeRegime = "keyless"
	regimeDiscontinued      freeRegime = "discontinued"
)

// freeBudgetGrantsAccess mirrors upstream `grantsFreeAccess`: every regime
// except `discontinued` still grants free access. Kept as an exhaustive switch
// so a newly added regime fails to compile rather than silently defaulting.
func freeBudgetGrantsAccess(r freeRegime) bool {
	switch r {
	case regimeRecurringDaily, regimeRecurringMonthly, regimeRecurringCredit,
		regimeRecurringUncapped, regimeOneTimeInitial, regimeKeyless:
		return true
	case regimeDiscontinued:
		return false
	default:
		return false
	}
}

// freeBudget is one catalogued free model (upstream `FreeModelBudget`, trimmed
// to the fields this gateway reads).
type freeBudget struct {
	Provider  string
	ModelID   string
	FreeType  freeRegime
	DisplayID string // friendly label, when it differs from ModelID
}

// freeModelCatalog is the shipped baseline, transcribed from OmniRoute's
// `freeModelCatalog.data.ts` for the providers this gateway routes. Entries
// whose regime is `discontinued` are kept in the source list (so the
// provenance is legible) and filtered out when the lookup tables are built.
var freeModelCatalog = []freeBudget{
	// Antigravity: keyless free tier.
	{"antigravity", "gemini-3.7-flash-high", regimeKeyless, ""},
	{"antigravity", "gemini-3.7-flash-medium", regimeKeyless, ""},
	{"antigravity", "gemini-3.7-flash-low", regimeKeyless, ""},
	{"antigravity", "gemini-pro-agent", regimeKeyless, ""},
	{"antigravity", "gemini-3.1-pro-low", regimeKeyless, ""},
	{"antigravity", "gemini-3.1-flash-lite", regimeKeyless, ""},
	{"antigravity", "claude-opus-4-6-thinking", regimeKeyless, ""},
	{"antigravity", "claude-sonnet-4-6", regimeKeyless, ""},
	{"antigravity", "gpt-oss-120b-medium", regimeKeyless, ""},

	// Z.AI GLM (upstream "glm" + "glm-cn"). The *-flash tier is free forever.
	{"glm", "glm-4.7-flash", regimeRecurringUncapped, ""},
	{"glm", "glm-4.5-flash", regimeRecurringUncapped, ""},
	{"glm-cn", "glm-4-flash", regimeRecurringUncapped, ""},
	{"glm-cn", "glm-4.5-flash", regimeRecurringUncapped, ""},
	{"glm-cn", "glm-4.7-flash", regimeRecurringUncapped, ""},
	{"glm-cn", "glm-signup-bonus", regimeOneTimeInitial, "Z.AI — 20M signup bonus"},

	// OpenCode (short alias "oc"). A keyless free tier: every free id is
	// reachable with the literal API key "public"
	// (KnownProviders["opencode"].DefaultAPIKey), which is why the provider is
	// filed under CategoryFree / AuthType "none" rather than freeTier.
	//
	// Every id below was verified by actually calling it (POST
	// /zen/v1/chat/completions with the literal key "public"), not merely read
	// off /zen/v1/models. That distinction matters: the listing advertises ids
	// the chat endpoint then refuses.
	//
	// Two ids from the previous list were removed for exactly that reason and
	// are deliberately NOT re-added by a later "sync with /models" pass:
	//   - deepseek-v4-flash-free -> 400 "Model is unavailable"
	//   - jev-1.13-free          -> 500 Internal server error
	// Seeding dead ids is worse than seeding fewer models: the operator hits a
	// failure on a model the router itself advertised, and every such request
	// burns a fallback attempt before reaching one that works.
	//
	// Note the suffix is "-free", NOT OpenRouter's ":free", so the payload
	// signal never fires and this catalogue entry is the only thing that can
	// badge these models.
	{"opencode", "big-pickle", regimeKeyless, ""},
	{"opencode", "mimo-v2.5-free", regimeKeyless, ""},
	{"opencode", "mimo-v2.6-flash-free", regimeKeyless, ""},
	{"opencode", "ling-3.0-flash-fin-free", regimeKeyless, ""},
	{"opencode", "nemotron-3-ultra-free", regimeKeyless, ""},
	{"opencode", "nemotron-3.5-lightning-free", regimeKeyless, ""},
	{"opencode", "space-bunny-free", regimeKeyless, ""},
	// muse-spark rejects max_output_tokens < 16 with a 400; the model itself is
	// fine with a sane value. See minOutputTokensForProvider.
	{"opencode", "muse-spark-1.3-contributor-free", regimeKeyless, ""},
	{"opencode", "muse-spark-1.2-contributor-free", regimeKeyless, ""},

	// Google AI Studio (api-key free tier).
	{"gemini", "gemini-2.5-flash", regimeRecurringDaily, ""},
	{"gemini", "gemini-2.5-flash-lite", regimeRecurringDaily, ""},
	{"gemini", "gemini-3-flash-preview", regimeRecurringDaily, ""},
	{"gemini", "gemini-3.1-flash-lite", regimeRecurringDaily, ""},

	// Cloudflare Workers AI (free allocation per day).
	{"cloudflare-ai", "@cf/mistral/mistral-7b-instruct-v0.2-lora", regimeRecurringDaily, ""},
	{"cloudflare-ai", "@cf/qwen/qwen2.5-coder-32b-instruct", regimeRecurringDaily, ""},
	{"cloudflare-ai", "@cf/deepseek-ai/deepseek-r1-distill-qwen-32b", regimeRecurringDaily, ""},
	{"cloudflare-ai", "@cf/meta/llama-3.3-70b-instruct-fp8-fast", regimeRecurringDaily, ""},
	{"cloudflare-ai", "@cf/meta/llama-3.2-3b-instruct", regimeRecurringDaily, ""},
	{"cloudflare-ai", "@cf/qwen/qwq-32b", regimeRecurringDaily, ""},
	{"cloudflare-ai", "@cf/zai-org/glm-4.7-flash", regimeRecurringDaily, ""},
	{"cloudflare-ai", "@cf/moonshotai/kimi-k2.6", regimeRecurringDaily, ""},
	{"cloudflare-ai", "@cf/google/gemma-4-26b-a4b-it", regimeRecurringDaily, ""},

	// NVIDIA NIM (developer-program free access).
	{"nvidia", "google/gemma-4-31b-it", regimeRecurringCredit, ""},
	{"nvidia", "nvidia/nemotron-3-super-120b-a12b", regimeRecurringCredit, ""},
	{"nvidia", "openai/gpt-oss-120b", regimeRecurringCredit, ""},
	// Missing from the earlier sync; OmniRoute re-audited NVIDIA NIM against
	// docs.api.nvidia.com (2026-10-30) and now files it recurring-uncapped
	// (request-rate limits only, no published token allowance). New rows take
	// the source regime; the older rows above keep theirs.
	{"nvidia", "nvidia/nemotron-3.5-super-vl-120b-a12b", regimeRecurringUncapped, ""},

	// Ollama Cloud free tier.
	{"ollama", "deepseek-v4-pro", regimeRecurringDaily, ""},
	{"ollama", "deepseek-v4-flash", regimeRecurringDaily, ""},
	{"ollama", "kimi-k2.6", regimeRecurringDaily, ""},
	{"ollama", "glm-5.1", regimeRecurringDaily, ""},
	{"ollama", "minimax-m2.7", regimeRecurringDaily, ""},
	{"ollama", "gemma4:31b", regimeRecurringDaily, ""},
	{"ollama", "nemotron-3-super", regimeRecurringDaily, ""},
	{"ollama", "qwen3.5:397b", regimeRecurringDaily, ""},

	// OpenRouter free tier (the ":free" models are handled by suffix too).
	{"openrouter", "liquid/lfm-2.5-2.6b:free", regimeRecurringDaily, ""},
	// Missing from the earlier sync (omni 2026-10-30).
	{"openrouter", "auto", regimeRecurringDaily, ""},
	{"openrouter", "stealth/ox-alpha", regimeRecurringDaily, ""},

	// Nous Research Portal free tier (verified against the live /v1/models feed).
	{"nous", "meituan/longcat-2.5-preview:free", regimeRecurringDaily, ""},
	{"nous", "meituan/longcat-2.0:free", regimeRecurringDaily, ""},
	{"nous", "stepfun/step-5-preview:free", regimeRecurringDaily, ""},
	{"nous", "stepfun/step-3.7-flash:free", regimeRecurringDaily, ""},
	{"nous", "upstage/solar-mini4:free", regimeRecurringDaily, ""},
	{"nous", "poolside/laguna-s-2.1:free", regimeRecurringDaily, ""},
	{"nous", "poolside/laguna-xs-2.1:free", regimeRecurringDaily, ""},

	// Vertex AI ("$300 free credits").
	{"vertex", "gemini-3.1-pro-preview", regimeOneTimeInitial, ""},
	{"vertex", "gemini-3.1-flash-lite", regimeOneTimeInitial, ""},
	{"vertex", "gemini-3-flash-preview", regimeOneTimeInitial, ""},
	{"vertex", "gemma-4-31b-it", regimeOneTimeInitial, ""},
	{"vertex", "DeepSeek-V4-Flash", regimeOneTimeInitial, ""},
	{"vertex", "DeepSeek-V4-Pro", regimeOneTimeInitial, ""},
	{"vertex", "Qwen3.6-35B-A3B", regimeOneTimeInitial, ""},
	{"vertex", "GLM-5.1-FP8", regimeOneTimeInitial, ""},
	{"vertex", "claude-opus-4-7", regimeOneTimeInitial, ""},
	{"vertex", "claude-sonnet-4-6", regimeOneTimeInitial, ""},
	// Missing from the earlier sync (omni 2026-10-30): the flash generations
	// below 3.1 and the MaaS lanes.
	{"vertex", "gemini-3.7-flash", regimeOneTimeInitial, ""},
	{"vertex", "gemini-3.6-flash", regimeOneTimeInitial, ""},
	{"vertex", "gemini-3.5-flash", regimeOneTimeInitial, ""},
	{"vertex", "gemini-3.5-flash-lite", regimeOneTimeInitial, ""},
	{"vertex", "gemini-2.5-pro", regimeOneTimeInitial, ""},
	{"vertex", "gemini-2.5-flash", regimeOneTimeInitial, ""},
	{"vertex", "gemini-2.5-flash-lite", regimeOneTimeInitial, ""},
	{"vertex", "deepseek-ai/deepseek-v3.2-maas", regimeOneTimeInitial, ""},
	{"vertex", "deepseek-ai/deepseek-v3.1-maas", regimeOneTimeInitial, ""},
	{"vertex", "qwen/qwen3-next-80b-a3b-instruct-maas", regimeOneTimeInitial, ""},
	{"vertex", "zai-org/glm-5-maas", regimeOneTimeInitial, ""},

	// Qoder (free plan models).
	{"qoder", "qwen3.8-max-preview", regimeRecurringDaily, ""},
	{"qoder", "qwen3.7-max", regimeRecurringDaily, ""},
	{"qoder", "qwen3.7-plus", regimeRecurringDaily, ""},
	{"qoder", "kimi-k3", regimeRecurringDaily, ""},
	{"qoder", "kimi-k2.7-code", regimeRecurringDaily, ""},
	{"qoder", "glm-5.2", regimeRecurringDaily, ""},
	{"qoder", "deepseek-v4-pro", regimeRecurringDaily, ""},
	{"qoder", "deepseek-v4-flash", regimeRecurringDaily, ""},
	{"qoder", "minimax-m3", regimeRecurringDaily, ""},

	// ---- Providers already in the registry but missing from the catalogue,
	// synced from OmniRoute freeModelCatalog.data.ts (2026-10-30), 2026-11-08.
	// Regimes are copied verbatim from the source; rows the source marks
	// `discontinued` are left out (they would be filtered anyway), and the
	// provider field uses the id this gateway routes with.

	// Agnes AI (docs wiki free flash lineup).
	{"agnes", "agnes-2.0-flash", regimeRecurringUncapped, ""},
	{"agnes", "agnes-2.5-flash", regimeRecurringUncapped, ""},
	{"agnes", "agnes-3.0-flash", regimeRecurringUncapped, ""},

	// Api-Airforce (free tier with 24M tokens/day pool).
	{"api-airforce", "x-ai/grok-3", regimeRecurringDaily, ""},
	{"api-airforce", "x-ai/grok-2-1212", regimeRecurringDaily, ""},
	{"api-airforce", "anthropic/claude-3.7-sonnet", regimeRecurringDaily, ""},
	{"api-airforce", "qwen/qwen3-32b", regimeRecurringDaily, ""},
	{"api-airforce", "moonshot/kimi-k2.6", regimeRecurringDaily, ""},
	{"api-airforce", "google/gemini-2.5-flash", regimeRecurringDaily, ""},
	{"api-airforce", "deepseek/deepseek-v3", regimeRecurringDaily, ""},

	// Baidu (ERNIE free tier).
	{"baidu", "ernie-4.0-8k", regimeRecurringUncapped, ""},

	// Bazaarlink (free gateway, 3.6M tokens/day pool).
	{"bazaarlink", "auto:free", regimeRecurringDaily, ""},
	{"bazaarlink", "claude-opus-4.7", regimeRecurringDaily, ""},
	{"bazaarlink", "claude-sonnet-4.6", regimeRecurringDaily, ""},
	{"bazaarlink", "claude-haiku-4.5", regimeRecurringDaily, ""},
	{"bazaarlink", "gpt-5.5", regimeRecurringDaily, ""},
	{"bazaarlink", "gpt-5.4", regimeRecurringDaily, ""},
	{"bazaarlink", "gpt-5.4-mini", regimeRecurringDaily, ""},
	{"bazaarlink", "gpt-5.4-nano", regimeRecurringDaily, ""},
	{"bazaarlink", "grok-4.3", regimeRecurringDaily, ""},
	{"bazaarlink", "grok-4.20", regimeRecurringDaily, ""},
	{"bazaarlink", "gemini-3.1-pro-preview", regimeRecurringDaily, ""},
	{"bazaarlink", "gemini-3-flash-preview", regimeRecurringDaily, ""},
	{"bazaarlink", "gemini-3.1-flash-lite-preview", regimeRecurringDaily, ""},
	{"bazaarlink", "gemma-4-31b-it", regimeRecurringDaily, ""},
	{"bazaarlink", "gemma-4-26b-a4b-it", regimeRecurringDaily, ""},
	{"bazaarlink", "deepseek-v3.2", regimeRecurringDaily, ""},
	{"bazaarlink", "kimi-k2.6", regimeRecurringDaily, ""},
	{"bazaarlink", "kimi-k2.5", regimeRecurringDaily, ""},
	{"bazaarlink", "glm-5.1", regimeRecurringDaily, ""},
	{"bazaarlink", "glm-5", regimeRecurringDaily, ""},
	{"bazaarlink", "mimo-v2.5-pro", regimeRecurringDaily, ""},
	{"bazaarlink", "mimo-v2.5", regimeRecurringDaily, ""},
	{"bazaarlink", "minimax-m2.7", regimeRecurringDaily, ""},
	{"bazaarlink", "minimax-m2.5", regimeRecurringDaily, ""},
	{"bazaarlink", "llama-4-maverick", regimeRecurringDaily, ""},
	{"bazaarlink", "llama-4-scout", regimeRecurringDaily, ""},
	{"bazaarlink", "llama-3.3-70b-instruct", regimeRecurringDaily, ""},
	{"bazaarlink", "qwen3.6-plus", regimeRecurringDaily, ""},
	{"bazaarlink", "mistral-large-2512", regimeRecurringDaily, ""},
	{"bazaarlink", "mistral-medium-3.1", regimeRecurringDaily, ""},
	{"bazaarlink", "mistral-small-2603", regimeRecurringDaily, ""},
	{"bazaarlink", "nemotron-3-super-120b-a12b", regimeRecurringDaily, ""},

	// Blackbox AI (keyless web tier).
	{"blackbox", "gpt-4o", regimeKeyless, ""},
	{"blackbox", "gemini-2.5-flash", regimeKeyless, ""},
	{"blackbox", "claude-sonnet-4", regimeKeyless, ""},
	{"blackbox", "deepseek-v3", regimeKeyless, ""},
	{"blackbox", "blackboxai", regimeKeyless, ""},
	{"blackbox", "blackboxai-pro", regimeKeyless, ""},

	// Bluesminds (free gateway, 7.2M tokens/day pool).
	{"bluesminds", "gpt-4o", regimeRecurringDaily, ""},
	{"bluesminds", "gpt-4o-mini", regimeRecurringDaily, ""},
	{"bluesminds", "gpt-4.1", regimeRecurringDaily, ""},
	{"bluesminds", "gpt-4.1-mini", regimeRecurringDaily, ""},
	{"bluesminds", "gpt-4.1-nano", regimeRecurringDaily, ""},
	{"bluesminds", "claude-sonnet-4-5", regimeRecurringDaily, ""},
	{"bluesminds", "claude-haiku-4-5", regimeRecurringDaily, ""},
	{"bluesminds", "gemini-2.0-flash", regimeRecurringDaily, ""},
	{"bluesminds", "gemini-2.0-flash-exp", regimeRecurringDaily, ""},
	{"bluesminds", "deepseek-reasoner", regimeRecurringDaily, ""},
	{"bluesminds", "deepseek-chat", regimeRecurringDaily, ""},
	{"bluesminds", "qwen-plus", regimeRecurringDaily, ""},
	{"bluesminds", "qwen-turbo", regimeRecurringDaily, ""},
	{"bluesminds", "kimi-k2", regimeRecurringDaily, ""},
	{"bluesminds", "kimi-k2-thinking", regimeRecurringDaily, ""},
	{"bluesminds", "glm-4.7", regimeRecurringDaily, ""},
	{"bluesminds", "glm-4-flash", regimeRecurringDaily, ""},
	{"bluesminds", "minimax-m2.5", regimeRecurringDaily, ""},
	{"bluesminds", "claude-opus-4-5", regimeRecurringDaily, ""},
	{"bluesminds", "gemini-2.5-pro", regimeRecurringDaily, ""},
	{"bluesminds", "grok-3", regimeRecurringDaily, ""},
	{"bluesminds", "qwen-max", regimeRecurringDaily, ""},

	// Cerebras ($5 one-time signup credit, 30-day expiry; omni #11773).
	{"cerebras", "zai-glm-4.7", regimeOneTimeInitial, ""},
	{"cerebras", "gpt-oss-120b", regimeOneTimeInitial, ""},

	// Cohere (trial keys, 800K tokens/month pool).
	{"cohere", "command-a-reasoning-08-2025", regimeRecurringMonthly, ""},
	{"cohere", "command-a-vision-07-2025", regimeRecurringMonthly, ""},
	{"cohere", "command-a-03-2025", regimeRecurringMonthly, ""},
	{"cohere", "command-r7b-12-2024", regimeRecurringMonthly, ""},
	{"cohere", "command-r-plus-08-2024", regimeRecurringMonthly, ""},
	{"cohere", "command-r-08-2024", regimeRecurringMonthly, ""},

	// DeepSeek (platform one-time credit).
	{"deepseek", "deepseek-v4-pro", regimeOneTimeInitial, ""},
	{"deepseek", "deepseek-v4-flash", regimeOneTimeInitial, ""},

	// Fireworks AI ($1 one-time trial credit).
	{"fireworks", "deepseek-v4-flash", regimeOneTimeInitial, ""},
	{"fireworks", "deepseek-v4-pro", regimeOneTimeInitial, ""},
	{"fireworks", "glm-5p1", regimeOneTimeInitial, ""},
	{"fireworks", "gpt-oss-120b", regimeOneTimeInitial, ""},
	{"fireworks", "gpt-oss-20b", regimeOneTimeInitial, ""},
	{"fireworks", "kimi-k2p5", regimeOneTimeInitial, ""},
	{"fireworks", "kimi-k2p6", regimeOneTimeInitial, ""},
	{"fireworks", "minimax-m2p5", regimeOneTimeInitial, ""},
	{"fireworks", "minimax-m2p7", regimeOneTimeInitial, ""},
	{"fireworks", "qwen3p6-plus", regimeOneTimeInitial, ""},

	// Groq (free plan: 200K tokens/day per model, hard 429; omni evidence
	// console.groq.com/docs/rate-limits 2026-09-02. The retired models llama-4-scout,
	// qwen3-32b and llama-3.3-70b-versatile are deliberately absent).
	{"groq", "openai/gpt-oss-120b", regimeRecurringDaily, ""},
	{"groq", "openai/gpt-oss-20b", regimeRecurringDaily, ""},
	{"groq", "openai/gpt-oss-safeguard-20b", regimeRecurringDaily, ""},
	{"groq", "qwen/qwen3.6-27b", regimeRecurringDaily, ""},
	{"groq", "qwen/qwen3.8-27b", regimeRecurringDaily, ""},

	// Hugging Face (Inference Providers monthly included credit).
	{"huggingface", "meta-llama/llama-3.1-8b-instruct", regimeRecurringMonthly, ""},
	{"huggingface", "meta-llama/llama-3.2-11b-instruct", regimeRecurringMonthly, ""},
	{"huggingface", "mistralai/mistral-7b-instruct", regimeRecurringMonthly, ""},
	{"huggingface", "google/gemma-2-9b-it", regimeRecurringMonthly, ""},
	{"huggingface", "Qwen/Qwen2.5-7B-Instruct", regimeRecurringMonthly, ""},
	{"huggingface", "deepseek-ai/DeepSeek-V3", regimeRecurringMonthly, ""},

	// Hyperbolic ($5 one-time signup credit).
	{"hyperbolic", "Qwen/QwQ-32B", regimeOneTimeInitial, ""},
	{"hyperbolic", "deepseek-ai/DeepSeek-R1", regimeOneTimeInitial, ""},
	{"hyperbolic", "deepseek-ai/DeepSeek-V3", regimeOneTimeInitial, ""},
	{"hyperbolic", "meta-llama/Llama-3.3-70B-Instruct", regimeOneTimeInitial, ""},
	{"hyperbolic", "meta-llama/Llama-3.2-3B-Instruct", regimeOneTimeInitial, ""},
	{"hyperbolic", "Qwen/Qwen2.5-72B-Instruct", regimeOneTimeInitial, ""},
	{"hyperbolic", "Qwen/Qwen2.5-Coder-32B-Instruct", regimeOneTimeInitial, ""},
	{"hyperbolic", "NousResearch/Hermes-3-Llama-3.1-70B", regimeOneTimeInitial, ""},

	// Kilo Gateway (free auto-router lane; trains on prompts per omni).
	{"kilo-gateway", "kilo-auto/free", regimeRecurringUncapped, ""},
	{"kilo-gateway", "tencent/hy3:free", regimeRecurringUncapped, ""},
	{"kilo-gateway", "stepfun/step-3.7-flash:free", regimeRecurringUncapped, ""},
	{"kilo-gateway", "openrouter/free", regimeRecurringUncapped, ""},
	{"kilo-gateway", "poolside/laguna-xs-2.1:free", regimeRecurringUncapped, ""},
	{"kilo-gateway", "poolside/laguna-m.1:free", regimeRecurringUncapped, ""},
	{"kilo-gateway", "cohere/north-mini-code:free", regimeRecurringUncapped, ""},
	{"kilo-gateway", "nvidia/nemotron-3-ultra-550b-a55b:free", regimeRecurringUncapped, ""},
	{"kilo-gateway", "nvidia/nemotron-3-super-120b-a12b:free", regimeRecurringUncapped, ""},
	{"kilo-gateway", "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free", regimeRecurringUncapped, ""},
	{"kilo-gateway", "nvidia/nemotron-3.5-content-safety:free", regimeRecurringUncapped, ""},
	{"kilo-gateway", "kwaipilot/kat-coder-pro-v2.5:free", regimeRecurringUncapped, ""},

	// Kiro (free plan, 25K tokens/month pool).
	{"kiro", "claude-sonnet-4.5", regimeRecurringMonthly, ""},
	{"kiro", "claude-haiku-4.5", regimeRecurringMonthly, ""},
	{"kiro", "deepseek-3.2", regimeRecurringMonthly, ""},
	{"kiro", "minimax-m2.5", regimeRecurringMonthly, ""},
	{"kiro", "minimax-m2.1", regimeRecurringMonthly, ""},
	{"kiro", "glm-5", regimeRecurringMonthly, ""},
	{"kiro", "qwen3-coder-next", regimeRecurringMonthly, ""},

	// LLM7.io (free gateway, 150M tokens/day pool on the flagship).
	{"llm7", "gpt-4o-mini-2024-07-18", regimeRecurringDaily, ""},
	{"llm7", "gpt-4.1-nano-2025-04-14", regimeRecurringDaily, ""},
	{"llm7", "deepseek-r1-0528", regimeRecurringDaily, ""},
	{"llm7", "qwen2.5-coder-32b-instruct", regimeRecurringDaily, ""},

	// Mistral (Free mode, 1B tokens/month pool; omni console-verified 2026-09-02).
	{"mistral", "mistral-large-latest", regimeRecurringMonthly, ""},
	{"mistral", "mistral-medium-3-5", regimeRecurringMonthly, ""},
	{"mistral", "mistral-small-latest", regimeRecurringMonthly, ""},
	{"mistral", "devstral-latest", regimeRecurringMonthly, ""},
	{"mistral", "codestral-latest", regimeRecurringMonthly, ""},

	// Morph (free plan, 400K tokens/month pool).
	{"morph", "morph-v3-large", regimeRecurringMonthly, ""},
	{"morph", "morph-v3-fast", regimeRecurringMonthly, ""},

	// Nebius AI Studio ($1 one-time trial credit).
	{"nebius", "meta-llama/Llama-3.3-70B-Instruct", regimeOneTimeInitial, ""},

	// OpenCode Zen (free lane on the PAYG gateway; ids as the registry lists
	// them — the "opencode/" prefix from the source catalogue is not part of
	// the id this gateway serves). jev-1.13-free stays out: the source still
	// lists it, but this gateway's own call evidence has it answering 500
	// (see the opencode block above — seeding dead ids is worse than seeding
	// fewer models).
	{"opencode-zen", "big-pickle", regimeRecurringUncapped, ""},
	{"opencode-zen", "beam-free", regimeRecurringUncapped, ""},
	{"opencode-zen", "mimo-v2.6-flash-free", regimeRecurringUncapped, ""},
	{"opencode-zen", "nemotron-3.5-super-vl-free", regimeRecurringUncapped, ""},
	{"opencode-zen", "nemotron-3.5-lightning-free", regimeRecurringUncapped, ""},
	{"opencode-zen", "longcat-2.5-preview-free", regimeRecurringUncapped, ""},
	{"opencode-zen", "deepseek-v4.1-flash-free", regimeRecurringUncapped, ""},
	{"opencode-zen", "glm-5.3-flash-free", regimeRecurringUncapped, ""},
	{"opencode-zen", "minimax-m3.1-flash-free", regimeRecurringUncapped, ""},

	// SambaNova (free tier, 200K tokens/day per model; omni evidence
	// docs.sambanova.ai 2026-10-10).
	{"sambanova", "DeepSeek-V3.2", regimeRecurringDaily, ""},
	{"sambanova", "Meta-Llama-3.3-70B-Instruct", regimeRecurringDaily, ""},
	{"sambanova", "gpt-oss-120b", regimeRecurringDaily, ""},
	{"sambanova", "DeepSeek-V3.1", regimeRecurringDaily, ""},
	{"sambanova", "gemma-4-31B-it", regimeRecurringDaily, ""},

	// SenseNova (one-time trial credit).
	{"sensenova", "sensechat", regimeOneTimeInitial, ""},

	// Tencent Cloud (Hunyuan free tier, concurrency-limited).
	{"tencent", "hunyuan-pro", regimeRecurringUncapped, ""},

	// TokenHarbor (free routes on a shared rolling 7-day allowance;
	// tokenharbor.ai/models 2026-10-29).
	{"tokenharbor", "deepseek-v4.1-flash:free", regimeRecurringUncapped, ""},
	{"tokenharbor", "mimo-v2.6-flash:free", regimeRecurringUncapped, ""},
}

var (
	freeCatalogOnce sync.Once
	// providersWithFreeModels is the set of provider ids that have at least one
	// documented free model.
	providersWithFreeModels map[string]struct{}
	// freeModelIDsByProvider maps a provider id to the set of catalogued free
	// model ids.
	freeModelIDsByProvider map[string]map[string]struct{}
)

func buildFreeCatalog() {
	providersWithFreeModels = make(map[string]struct{})
	freeModelIDsByProvider = make(map[string]map[string]struct{})
	for _, entry := range freeModelCatalog {
		if !freeBudgetGrantsAccess(entry.FreeType) {
			continue
		}
		providersWithFreeModels[entry.Provider] = struct{}{}
		ids := freeModelIDsByProvider[entry.Provider]
		if ids == nil {
			ids = make(map[string]struct{})
			freeModelIDsByProvider[entry.Provider] = ids
		}
		ids[entry.ModelID] = struct{}{}
	}
}

// ProviderHasFreeModels reports whether the provider (id or alias) exposes any
// documented free model.
func ProviderHasFreeModels(providerID string) bool {
	if providerID == "" {
		return false
	}
	freeCatalogOnce.Do(buildFreeCatalog)
	if _, ok := providersWithFreeModels[providerID]; ok {
		return true
	}
	_, ok := providersWithFreeModels[ResolveAlias(providerID)]
	return ok
}

// isCatalogFreeModel reports whether the model id is listed as free for the
// provider (id or alias) in the shipped catalogue.
func isCatalogFreeModel(providerID, modelID string) bool {
	if modelID == "" {
		return false
	}
	freeCatalogOnce.Do(buildFreeCatalog)
	if _, ok := freeModelIDsByProvider[providerID][modelID]; ok {
		return true
	}
	_, ok := freeModelIDsByProvider[ResolveAlias(providerID)][modelID]
	return ok
}

// FreeRegimeFor reports the documented regime of a provider's free tier, as
// the shipped catalogue records it ("recurring-daily", "recurring-monthly",
// "recurring-credit", "recurring-uncapped", "one-time-initial", "keyless").
//
// This is the hook the regime-aware recovery scheduler reads: WHEN a free
// quota comes back is a property of the tier, the catalogue is where that
// property is documented, so the recovery math never guesses. An exact
// (provider, model) entry wins; without one, the provider's first catalogued
// entry stands in — a provider's rows share one regime in every catalogue
// block, so the fallback is safe. Discontinued entries do not count (they
// never granted access anyway). ok is false when nothing is catalogued.
func FreeRegimeFor(providerID, modelID string) (string, bool) {
	if providerID == "" {
		return "", false
	}
	freeCatalogOnce.Do(buildFreeCatalog)

	lookup := func(pid, mid string) (freeRegime, bool) {
		if mid != "" {
			if _, listed := freeModelIDsByProvider[pid][mid]; listed {
				for _, entry := range freeModelCatalog {
					if entry.Provider == pid && entry.ModelID == mid && freeBudgetGrantsAccess(entry.FreeType) {
						return entry.FreeType, true
					}
				}
			}
		}
		for _, entry := range freeModelCatalog {
			if entry.Provider == pid && freeBudgetGrantsAccess(entry.FreeType) {
				return entry.FreeType, true
			}
		}
		return "", false
	}

	if regime, ok := lookup(providerID, modelID); ok {
		return string(regime), true
	}
	if alias := ResolveAlias(providerID); alias != "" && alias != providerID {
		if regime, ok := lookup(alias, modelID); ok {
			return string(regime), true
		}
	}
	return "", false
}

// FreeCatalogProviders lists every provider id the shipped catalogue covers —
// the roster the health endpoint and the fallback chain enumerate.
func FreeCatalogProviders() []string {
	freeCatalogOnce.Do(buildFreeCatalog)
	out := make([]string, 0, len(providersWithFreeModels))
	for pid := range providersWithFreeModels {
		out = append(out, pid)
	}
	sort.Strings(out)
	return out
}

// FreeCatalogModelCounts maps every catalogued provider id to the number of
// free models the catalogue lists for it. Read-only copy.
func FreeCatalogModelCounts() map[string]int {
	freeCatalogOnce.Do(buildFreeCatalog)
	out := make(map[string]int, len(providersWithFreeModels))
	for _, entry := range freeModelCatalog {
		if !freeBudgetGrantsAccess(entry.FreeType) {
			continue
		}
		out[entry.Provider]++
	}
	return out
}

// FreeModelCandidate is the subset of a model entry the free check reads.
type FreeModelCandidate struct {
	ID          string
	DisplayName string
	IsFree      *bool
	// Free is a provider-supplied `free` field; a truthy-but-not-`true` value
	// (e.g. the string "false") is NOT a signal, matching upstream.
	Free any
	// PromptPrice / CompletionPrice are upstream usage prices, when reported.
	PromptPrice     string
	CompletionPrice string
}

func zeroPrice(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	// Accept "0", "0.0", "0.000000" and friends without pulling in strconv's
	// error path: everything but digits and a dot means "not a number".
	for _, r := range value {
		if r != '.' && (r < '0' || r > '9') {
			return false
		}
	}
	for _, r := range value {
		if r >= '1' && r <= '9' {
			return false
		}
	}
	return true
}

// hasPayloadFreeSignal mirrors upstream `hasPayloadFreeSignal`.
func hasPayloadFreeSignal(model FreeModelCandidate) bool {
	if model.IsFree != nil && *model.IsFree {
		return true
	}
	if strings.HasSuffix(model.ID, ":free") {
		return true
	}
	return zeroPrice(model.PromptPrice) && zeroPrice(model.CompletionPrice)
}

// IsFreeModel reports whether a single model qualifies as free for the given
// provider (id or alias): a catalogue entry, or a payload signal on a provider
// with a documented free tier.
func IsFreeModel(providerID string, model FreeModelCandidate) bool {
	if isCatalogFreeModel(providerID, model.ID) {
		return true
	}
	return ProviderHasFreeModels(providerID) && hasPayloadFreeSignal(model)
}

// IsModelFreeBadge mirrors upstream `isModelFreeBadge` with the default
// (non-strict) rule, which is what the provider page uses: any truthy `free`
// field, a `:free` id suffix, "free"/"grátis" in the display name, or
// `IsFreeModel`.
func IsModelFreeBadge(providerID string, model FreeModelCandidate) bool {
	if truthy(model.Free) {
		return true
	}
	if strings.HasSuffix(model.ID, ":free") {
		return true
	}
	if containsFreeWord(model.DisplayName) {
		return true
	}
	return IsFreeModel(providerID, model)
}

// truthy covers the values a JSON-ish `free` field can carry. Only boolean true
// and the strings "true"/"free" count — upstream explicitly excludes a
// truthy-but-not-`true` value such as the string "false".
func truthy(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "true" || s == "free"
	default:
		return false
	}
}

// containsFreeWord matches upstream's `/\bgr[aá]tis\b|\bfree\b/i`.
func containsFreeWord(name string) bool {
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	for _, needle := range []string{"free", "grátis", "gratis"} {
		idx := strings.Index(lower, needle)
		for idx >= 0 {
			before := byte(' ')
			if idx > 0 {
				before = lower[idx-1]
			}
			after := byte(' ')
			if idx+len(needle) < len(lower) {
				after = lower[idx+len(needle)]
			}
			if !isWordByte(before) && !isWordByte(after) {
				return true
			}
			next := strings.Index(lower[idx+1:], needle)
			if next < 0 {
				break
			}
			idx = idx + 1 + next
		}
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b >= 0x80
}
