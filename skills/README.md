# 9RouterX — Agent Skills

Drop-in skills for any AI agent (Claude, Cursor, ChatGPT, custom SDK). Just **copy a link** below and paste it to your AI — it will fetch the skill and use 9RouterX for you.

> Tip: start with the **9routerx** entry skill — it covers setup and links to all capability skills.

## Skills

| Capability | Copy link below and paste to your AI |
|---|---|
| **Entry / Setup** (start here) | https://raw.githubusercontent.com/yanstd888/9routerx/refs/heads/main/skills/9routerx/SKILL.md |
| Chat / code-gen | https://raw.githubusercontent.com/yanstd888/9routerx/refs/heads/main/skills/9routerx-chat/SKILL.md |
| Image generation | https://raw.githubusercontent.com/yanstd888/9routerx/refs/heads/main/skills/9routerx-image/SKILL.md |
| Video generation (xAI Grok Imagine) | https://raw.githubusercontent.com/yanstd888/9routerx/refs/heads/main/skills/9routerx-video/SKILL.md |
| Text-to-speech | https://raw.githubusercontent.com/yanstd888/9routerx/refs/heads/main/skills/9routerx-tts/SKILL.md |
| Speech-to-text | https://raw.githubusercontent.com/yanstd888/9routerx/refs/heads/main/skills/9routerx-stt/SKILL.md |
| Embeddings | https://raw.githubusercontent.com/yanstd888/9routerx/refs/heads/main/skills/9routerx-embeddings/SKILL.md |
| Web search | https://raw.githubusercontent.com/yanstd888/9routerx/refs/heads/main/skills/9routerx-web-search/SKILL.md |
| Web fetch (URL → markdown) | https://raw.githubusercontent.com/yanstd888/9routerx/refs/heads/main/skills/9routerx-web-fetch/SKILL.md |

## How to use

Paste to your AI (Claude, Cursor, ChatGPT, …):

```
Read this skill and use it: https://raw.githubusercontent.com/yanstd888/9routerx/refs/heads/main/skills/9routerx/SKILL.md
```

Then ask normally — *"generate an image of a cat"*, *"transcribe this URL"*, etc.

## Configure your shell once

```bash
export 9ROUTERX_URL="http://localhost:20128"   # local default, or your VPS / tunnel URL
export 9ROUTERX_KEY="sk-..."                   # from Dashboard → Keys (only if requireApiKey=true)
```

Verify: `curl $9ROUTERX_URL/api/health` → `{"ok":true}`.

## Links

- Source: https://github.com/yanstd888/9routerx
- Dashboard: https://9router.com
