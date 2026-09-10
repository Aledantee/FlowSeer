# Registry evidence

One line per claim the registry relies on: model, claim, source, date read.
`tune` appends here; a registry field without a line here is an opinion.

## Prices and context

- All models — list prices and context windows — models.dev `api.json`, cross-checked against OpenRouter `/api/v1/models` — 2026-09-09. Vendor wins where they differ: GPT-5.6 Sol is 4/20 at OpenAI (8/30 past 272K context) against 2/10 at the broker; DeepSeek V4 Flash is 0.14/0.28 at DeepSeek against 0.07–0.09/0.18 at brokers.
- GLM-5.3 and GLM-5.3 Flash — context 1M tokens, 128K max output, at the vendor; the 1,310,720 figure is OpenRouter's — https://docs.z.ai/guides/llm/glm-5.3 and models.dev `zai` rows — 2026-09-09.
- DeepSeek V4 Pro — 0.435/0.87 per million at DeepSeek (models.dev `deepseek` row, 0813 checkpoint), 0.66/1.98 on the OpenCode Go feed, 0.95/1.91 at OpenRouter; context 1M — models.dev `api.json` — 2026-09-09.
- Qwen3.8 Flash — 0.15/0.47 per million at Alibaba, context 1M; same price on the OpenCode Go feed and at OpenRouter — models.dev `api.json` — 2026-09-09.
- Grok 4.6 — 2/6 per million at xAI, context 500K; same on the OpenCode Go feed — models.dev `api.json` — 2026-09-09.
- GPT-6 Astra Pro — a separate id from `gpt-6-astra`, whose `openai` row on models.dev is the registry's 10/50; the Pro variant is 10/50 on OpenRouter and Kilo since 2026-09-04 with no `openai` vendor row on models.dev and no API id in OpenAI's launch note, so it stays out of the registry until it has a vendor price — https://openai.com/index/gpt-6-astra/ and OpenRouter `/api/v1/models` — 2026-09-09.
- OpenCode Go — $10/month buys $60 of usage at list rates, metered $12 per 5 hours and $30 per week; Luna and Grok 4.5 capped at $15/month each — https://www.bitdoze.com/opencode-go-plan/ and https://llmgateway.io/blog/opencode-go-pricing — 2026-09-09.

## Refusal posture

- Claude Fable 5 / 5.1 — `stop_reason: refusal` with categories `cyber`, `bio`, `frontier_llm`, `reasoning_extraction`; the cyber category fires on exploit and intrusion tooling, so authorized security work trips it; Claude Code routes a trigger to Opus 4.8 silently — https://www.developersdigest.tech/blog/fable-5-safeguards-refusal-architecture and https://kenhuangus.substack.com/p/claude-fable-5-part-8-the-refusal — 2026-09-09.
- GPT-6 Astra — first OpenAI model rated Critical for cyber; refuses 91.5% on cyber jailbreak evaluations against 59% for GPT-5.6 Sol; blocks proof-of-concept exploits, allows secure code review and patching — https://deploymentsafety.openai.com/gpt-6-astra and https://app.stationx.net/articles/gpt-6-astra-security — 2026-09-09.
- Gemini 3.8 Flash Cyber and GPT-5.6-Cyber — permissive variants gated to vetted defender teams (Fairwind, Daybreak); product and general engineering teams do not qualify — https://blog.google/innovation-and-ai/models-and-research/gemini-models/3-8-flash-and-3-8-flash-cyber/ and https://www.axios.com/2026/08/10/openai-gpt-astra-restrictions-safety-hacking-defenders — 2026-09-09.

## Benchmarks (harness named where the source names it)

- GLM-5.3 — Terminal-Bench 2.1 88.2 — https://kingy.ai/blog/glm-5-3-vs-kimi-k3-vs-deepseek-v4-pro/ — 2026-09-09.
- Kimi K3 — Terminal-Bench 2.1 88.3 — same source — 2026-09-09.
- Qwen3.8 Max — Terminal-Bench 2.1 86.6, hosted service, checkpoint unverified — same source — 2026-09-09.
- MiniMax M3 — Terminal-Bench 2.1 66.0; SWE-bench Pro 59.0 self-reported with Claude Code scaffolding — https://www.minimax.io/blog/minimax-m3 and https://artificialanalysis.ai/models/minimax-m3 — 2026-09-09.
- Gemini 3.8 Flash — beats Gemini 3.1 Pro on Google's public coding lane (67.7 vs 46.3) and agentic lane (66.5 vs 39.6); "Opus 5 parity" is a vendor claim — https://www.vellum.ai/blog/gemini-3-8-flash-benchmarks-explained — 2026-09-09.
- DeepSeek V4 Pro 0813 — Terminal-Bench 2.1 87.9 (up from 72.1 on the April preview); SWE-bench Verified 96.4 on the Vals AI board, harness Vals' own — https://www.mindstudio.ai/blog/deepseek-v4-pro-0813-benchmarks and https://www.vals.ai/models/deepseek_deepseek-v4-pro-0813 — 2026-09-09.
- Qwen3.8 Flash — no Terminal-Bench 2.1 or SWE-bench Verified number found; Qwen3.8-Flash-Next reports SWE-bench Pro 62.5 (self-reported), a different model and harness — https://www.datacamp.com/blog/qwen3-8-flash-next — 2026-09-09.
- GPT-6 Astra — Terminal-Bench 4.0 57.7 against GPT-5.6 Sol 37.3 and Terminal-Bench Science 0.1 64.6 against Claude Fable 5.1 52.6, OpenAI's own runs; not Terminal-Bench 2.1, so not comparable with the `terminal_bench` column — https://openai.com/index/gpt-6-astra/ — 2026-09-09.
- GPT-5.6 Sol — Artificial Analysis Coding Agent Index 80 — https://openai.com/index/gpt-5-6/ — 2026-09-09.

## Harness behaviour found by calibration

- `discover-host.sh` inside the Claude Code sandbox reports `google`, `go`, and `zen` as signed out and exits 1: `opencode` cannot open its log file under `~/.local/share/opencode/log` and `agy models` fails the same way. Unsandboxed the same run reports all five pools signed in. Run it unsandboxed, as `delegate` already says for `orca` — 2026-09-09.

- `opencode run` (1.18.25 and 1.18.30) never returns headless: it logs `init` and stops, with MCP disabled, in a plain directory, with `OPENCODE_CONFIG_DIR` unset, and with `--attach`. `opencode serve` plus `POST /session/{id}/message` works and reports cost and tokens per message; `bench.sh` uses that path — 2026-09-09.
- `agy -p` returns partial output after 5 minutes by default; pass `--print-timeout` for any unit that runs longer — 2026-09-09.
- `codex exec` needs `--skip-git-repo-check` outside a repository and reads stdin unless it is closed; the user's global compound-engineering plugin makes it read its workflow files before the first edit — 2026-09-09.
- `claude -p` refuses to start with `CLAUDECODE` set in the environment; `bench.sh` unsets it — 2026-09-09.

## Calibration 2026-09-09 — `Merge` task (`tune/references/calibration.md`)

Execute (7 hidden acceptance tests, `-race`, verifier): Gemini 3.8 Flash high 7/7 in 201 s ($0.56 list, plan-covered); Kimi K3 on Go 7/7 in 1,235 s ($0.82 of the Go window); GPT-5.6 Sol high 7/7 on the staged files, uncommitted after 110 min because the user's global compound-engineering Codex plugin ran its own review workflow; Claude Sonnet 5 6/7 in 269 s ($0.69 list, plan-covered) with a deadlock on the source-error path that its own tests hid by hand-calling `Done`; GLM-5.3 on Go 0/7, 32K reasoning tokens then `finish: length` with no tool call (902 s, $0.17).

Review of the Sonnet diff (known deadlock as ground truth): Opus 5 found it plus one valid extra (148 s, $0.66); Sol found it plus four (530 s, ~$3.5 list); Gemini 3.8 Flash found it plus four (275 s, $0.48); Sonnet 5 missed it and asserted no forwarder can block (140 s, $0.31); Qwen3.8-Max on Go missed it, one valid medium (1,669 s, $0.63).
