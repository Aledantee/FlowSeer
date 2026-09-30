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

Limits of this calibration, found 2026-09-18 by reading `bench.sh` and this file; the raw outputs were not kept:

- The three Claude lanes (Sonnet 5 execute, Sonnet 5 review, Opus 5 review) ran at the CLI's default effort. `bench.sh` accepted `--effort` and did not pass it to `claude -p`; it does now. Gemini and Sol ran at `high`. No lane ran at `xhigh`, the level the `execute` and `review-unit` roles route at, so Sonnet 5 at `xhigh` is unmeasured in either direction.
- Each lane ran once. The fit-set changes made on these results (GLM-5.3 out of `execute`, Sonnet 5 and Qwen3.8-Max out of `review-unit`) rest on one sample each.
- The review ground truth was Sonnet 5's own diff, so Sonnet 5's miss is a self-review, and Opus 5's hit is a same-vendor pairing that `review-unit` never dispatches.
- The base commit was not recorded, although `calibration.md` requires it. `Merge` landed in d4421211 the same day; a lane branched after it measured verification, not implementation.

## Calibration 2026-09-18 — `Merge` task, Claude Sonnet 5 `execute` at `xhigh`

Base bd9e0862 (`d4421211^`, so the lanes measure implementation), two runs through `bench.sh --cli claude --model claude-sonnet-5 --effort xhigh`, graded with each of the 7 hidden acceptance tests run on its own under `-race -count=3`, because a `synctest` deadlock panic ends the test binary and hides the tests after it. Run 1: 6/7 in 591 s, $1.58 reported (48K thinking tokens). Run 2: 5/7 in 580 s, $1.39 reported (45K thinking tokens). Both fail `TestMergeAcceptSourceErrorPropagates` with "all goroutines in bubble are blocked", the failure the default-effort run had on 2026-09-09; run 2 also fails `TestMergeAcceptContextCancelPropagates`. The verifier was not run on either lane, since neither passed acceptance. `xhigh` doubled wall time and cost against the default-effort run (269 s, $0.69) and did not remove the deadlock. Gemini 3.8 Flash and Kimi K3 were not re-run; their 7/7 results are one sample each on an unrecorded base.

## Synthetic pool smoke test 2026-09-19

One `opencode serve` (1.18.30) in an empty directory, one session per `synthetic/` id from `opencode models`, default agent, one prompt: read `probe.txt` with the file tool and reply with its text. A pass means the reply carried the file's text, which the model could only get through a tool call. Each result is one run; `cost` is what opencode reported at list rates, not what the subscription charged.

- `opencode auth list` shows Synthetic signed in; `opencode models` lists ten `synthetic/hf:` ids — 2026-09-19.
- hf:moonshotai/Kimi-K3 — pass, 15 s, 16.6K input tokens uncached, cost 0.051 — 2026-09-19.
- hf:zai-org/GLM-5.3-Flash — pass, 9 s, cost 0.0026 — 2026-09-19.
- hf:deepseek-ai/DeepSeek-V4.1-Flash — pass, 19 s, cost 0.0008 — 2026-09-19.
- hf:openai/gpt-oss-120b — pass, 8 s, cost 0.0015 — 2026-09-19.
- hf:nvidia/NVIDIA-Nemotron-3-Super-120B-A12B-NVFP4 — pass, 8 s, cost 0.0059 — 2026-09-19.
- hf:MiniMaxAI/MiniMax-M3, hf:moonshotai/Kimi-K2.7-Code, hf:Qwen/Qwen3.6-27B, hf:zai-org/GLM-5.2 — HTTP 404 from Synthetic, "is no longer supported. Try using a different model, like hf:moonshotai/Kimi-K3". opencode's catalogue still lists them, so `opencode models` overstates what the pool serves — 2026-09-19.
- hf:zai-org/GLM-4.7-Flash — no reply within 240 s, no error — 2026-09-19.
- Synthetic quota — `GET https://api.synthetic.new/v2/quotas` is documented to return `subscription.{limit, requests, renewsAt}` and not to count against the limit — https://dev.synthetic.new/docs/synthetic/quotas — 2026-09-19.
- Synthetic quota, live reply — also returns `rollingFiveHourLimit.{remaining, max, limited, nextTickAt, tickPercent}` and `weeklyTokenLimit.{percentRemaining, maxCredits, remainingCredits, nextRegenAt}`, which the docs page does not list. After the ten probes above `subscription.requests` was still 0 of 2500 while `rollingFiveHourLimit.remaining` read 2495.78 of 2500 and weekly credits $119.86 of $120.00, so `pool-usage.sh` meters the latter two. The fractional remainder means requests are weighted; the weights and the tick interval are not measured — 2026-09-19.

## Refresh 2026-09-23

Prices and context:

- All models — list prices and context re-pulled by `catalogue.py` from models.dev `api.json` and OpenRouter `/api/v1/models` — 2026-09-23. Vendor rows win; brokers differ by more than 20% on GPT-5.6 Sol (OpenRouter 2/10 against OpenAI 4/20), GLM-5.3 (0.84/2.64 against Z.ai 1.4/4.4), GLM-5.2 (0.65/2.04 against 1.4/4.4), Kimi K2.7 Code (0.71/3.30 against Moonshot 0.95/4), Qwen3.6 27B (0.32/2.70 against Alibaba 0.6/3.6), DeepSeek V4 Pro (0.96/1.91 against DeepSeek 0.435/0.87), and DeepSeek V4 Flash (0.09/0.18 against DeepSeek 0.15/0.6).
- Claude Opus 5.5 — 4/20 per million, context 1M, released 2026-09-22 — models.dev `anthropic` row and https://www.anthropic.com/claude-opus-5-5 — 2026-09-23.
- GPT-6 Sol 2/10 and GPT-6 Luna 0.1/0.5 per million, context 1,050,000, released 2026-09-22 — models.dev `openai` rows and https://openai.com/index/introducing-gpt-6-sol-and-luna/ — 2026-09-23. GPT-6 Sol Pro and GPT-6 Luna Pro appear on OpenRouter with no `openai` vendor row, so they stay out, as GPT-6 Astra Pro did.
- GLM-5.3 Flash — 0.15/0.5 at Z.ai, up from 0.07/0.25 on 2026-09-09 — models.dev `zai` row — 2026-09-23.
- DeepSeek V4 Flash — DeepSeek's row now reads 0.15/0.6, context 1M, released 2026-09-10; DeepSeek serves the `deepseek-v4-flash` name as V4.1 Flash since that date, so the V4 Flash weights are retired at the vendor — models.dev `deepseek` row, https://dataconomy.com/2026/09/11/deepseek-v4-1-flash-ultralow-token-pricing/ — 2026-09-23. The alias claim comes from deepseek.ai/pricing, which is not DeepSeek's own domain (deepseek.com); treat it as unconfirmed until api-docs.deepseek.com says so.
- DeepSeek V4.1 Flash — 0.15/0.6 off-peak, doubled in peak hours, context 1M, open weights (MIT), launched 2026-09-10 — same sources — 2026-09-23. OpenRouter lists 0.10/0.50.
- Kimi K2.7 Code 0.95/4 and Qwen3.6 27B 0.6/3.6 at context 262,144 (the registry had 1M for both, unsourced); GLM-5.2 1.4/4.4 at 1M; Nemotron 3 Super 0.2/0.8 at 262,144 from the `nvidia` row; gpt-oss-120b has no `openai` row, so the registry carries Synthetic's 0.1/0.1 at 131,072; GLM-4.7 Flash 0/0 at 200K from the `zai` row — models.dev `api.json` — 2026-09-23.
- Grok 4.7 — 2/6 at xAI, context 500K, released 2026-09-21; not added, since no signed-in pool other than per-token `zen` serves it, the same standing as Grok 4.6 — models.dev `xai` row, https://www.marktechpost.com/2026/09/21/spacexai-releases-grok-4-7/ — 2026-09-23.

Pools and CLIs:

- Codex CLI 0.155.1 — `codex debug models` lists gpt-6-astra, gpt-6-sol, and gpt-5.6-sol with efforts low through `ultra`, and gpt-6-luna and gpt-5.6-luna with low through `max` (the registry had Luna 5.6 at low–high) — 2026-09-23. `ultra` is described as "maximum reasoning with automatic task delegation"; no role routes at it.
- agy 1.2.9 — `agy models` still offers gemini-3.8-flash at low, medium, high only — 2026-09-23.
- Synthetic served list — `GET https://api.synthetic.new/openai/v1/models` lists gpt-oss-120b, GLM-5.3-Flash, DeepSeek-V4.1-Flash, Kimi-K3, Qwen3.8-27B, GLM-4.7-Flash, Nemotron-3-Super, and four `syn:` aliases; MiniMax-M3, Kimi-K2.7-Code, Qwen3.6-27B, and GLM-5.2 are absent. Kimi-K3, GLM-5.3-Flash, and DeepSeek-V4.1-Flash are served at 524,288 context, half their vendor windows — 2026-09-23.
- Synthetic silent aliasing — the four absent ids, which answered HTTP 404 on 2026-09-19, each passed the one-request file-read probe today with no error and near-identical reported costs. A probe no longer shows whether Synthetic still serves an id; its `/openai/v1/models` list does — 2026-09-23.
- opencode agents — `~/.config/opencode/opencode.json` was rewritten 2026-09-19 20:44 to model-named, pin-only agents (the claude, codex-model, and gemini ids on `zen`; `kimi-k3` and `glm-5.3-flash` on `synthetic`); the registry committed 7 minutes later still named the earlier role agents (`execute-open`, `research`, `critique`, `overflow-frontier`, `execute-*`), none of which exists now. The registry now names the live agents — 2026-09-23.

Refusal posture and benchmarks:

- Claude Opus 5.5 — Anthropic's cyber classifiers route a trigger to Opus 4.8 rather than refusing; the only write-up found reuses Opus 5 findings, so `medium` is carried over from Opus 5 and is not measured for 5.5 — https://neuraltrust.ai/blog/claude-opus-5-5-security-safety — 2026-09-23.
- Claude Opus 5.5 — Terminal-Bench 4.0 66.4 at xhigh (Anthropic, harness unnamed); no Terminal-Bench 2.1 or SWE-bench Verified number found — https://www.anthropic.com/claude-opus-5-5 — 2026-09-23.
- GPT-6 Sol — no cyber refusal figure published; OpenAI says it "closes most of the alignment gap with Astra", so it may refuse more than GPT-5.6 Sol (59%) — `refusal_cyber: null` until measured — https://thenewstack.io/gpt-sol-alignment-gaps/ — 2026-09-23.
- GPT-6 Sol — available in Codex for most paid accounts; DeepSWE v1.1 68.8 at max effort (harness unnamed); no Terminal-Bench 2.1 or SWE-bench Verified number found — https://artificialanalysis.ai/models/gpt-6-sol — 2026-09-23.
- GPT-6 Luna — rolling out to Codex; some users report it missing from the picker at launch (it is listed on this host); no refusal or Terminal-Bench 2.1 figures found — https://community.openai.com/t/gpt-6-luna-not-available-in-codex/1399923 — 2026-09-23.
- Grok 4.7 — Terminal-Bench 4.0 26 (harness unnamed); no Terminal-Bench 2.1 found — https://the-decoder.com/xai-launches-grok-4-7-at-bargain-prices-but-benchmarks-reveal-a-wide-gap-to-claude-and-gpt-6/ — 2026-09-23.

## Calibration 2026-09-23 — `Merge` task, GPT-6 Sol `execute` at `xhigh`

Base bd9e0862 (`d4421211^`, so the lane measures implementation), one run through `bench.sh --cli codex --model gpt-6-sol --effort xhigh` on codex-cli 0.155.1, with the user's global compound-engineering plugin disabled for the lane (`bench.sh` now passes that override on every codex lane). The model added `merge.go` (107 lines) and `merge_test.go` (282 lines) and committed them in 371 s. Graded with each of the 7 hidden acceptance tests run on its own under `-race -count=3`, then together: 7/7. The verifier on both changed paths passed. Usage 1,195,959 input tokens (1,121,920 of them cache reads), 15,799 output (9,420 reasoning); no dollar figure from the CLI, so $0.53 is estimated at list price with cache reads at a tenth of input. The codex weekly window still read 0% afterwards. One sample, same base as the 2026-09-18 Sonnet 5 runs (5/7 and 6/7 at 580–591 s, $1.39–1.58), so the two compare; GPT-5.6 Sol's 7/7 of 2026-09-09 ran on an unrecorded base with the plugin active and does not.

## Calibration 2026-09-23 — `Merge` task, every claude, codex, and google model

Execute lanes from base bd9e0862 on `calibration/brief.md`; review lanes on commit 30c8da74 (a 2026-09-09 Claude Sonnet 5 `Merge` that deadlocks when a source fails while a sibling's producer stops without closing its data channel: the forwarder never watches the source's `Stopped()`, so `wg.Wait` never returns), with a brief that states the contract and asks for findings. Effort `xhigh` where the model lists it, `high` on Gemini, none on Haiku. Execute graded with each of the 7 hidden acceptance tests alone under `-race -count=3`, then the verifier on the changed paths for every 7/7 lane (all six passed). Each lane ran once. Cost is the CLI's figure on claude and an estimate from reported tokens at list price elsewhere ("est"). Quota is the change in the pool's own meter across the lane with the pool's lanes run one at a time; claude and codex meters report whole percents, so 0 means under one percent; this coordinating session also drew on the claude pool.

- Execute 7/7 and verifier green: GPT-6 Sol 371 s ~$0.53 codex +0%; Gemini 3.8 Flash high 453 s ~$1.17 gemini 5h +0.73%; Claude Fable 5.1 524 s $3.87 claude +0%; GPT-5.6 Luna 612 s ~$0.13 codex +0%; GPT-6 Astra 782 s ~$3.25 codex weekly +1%; GPT-6 Luna 1,336 s ~$0.05 codex +0% — 2026-09-23.
- Execute failures, all on `TestMergeAcceptSourceErrorPropagates`: Claude Opus 5.5 6/7 (552 s, $2.26), Claude Opus 5 6/7 (428 s, $2.08), Claude Haiku 4.5 6/7 (450 s, $0.51), Claude Sonnet 5 4/7 (440 s, $0.88; also fails consumer-stop and context-cancel). Every Anthropic model but Fable 5.1 wrote the deadlock the review lanes look for — 2026-09-23.
- Review, found the deadlock: Claude Opus 5.5 218 s $0.95 +4 valid extra; GPT-6 Sol 256 s ~$0.27 +3; GPT-6 Astra 263 s ~$1.70 +3; Gemini 3.8 Flash 374 s ~$0.74 +2; Claude Fable 5.1 418 s $2.99 +1 (rated medium, unsure it breaks the contract); Claude Opus 5 608 s $2.36 +3 (rated medium); GPT-5.6 Luna 711 s ~$0.11 +2 — 2026-09-23.
- Review, missed it: Claude Sonnet 5 555 s $1.22 (its stress test hung on the bug and it blamed its own producer); GPT-6 Luna 265 s ~$0.02 (one valid finding, lost cancellation); Claude Haiku 4.5 192 s $0.23 (one invalid finding) — 2026-09-23.
- Not measured: GPT-5.6 Sol (dropped for time; GPT-6 Sol covers its roles at half the price). Synthetic models deferred to a later run; Kimi K3 6/7 (fails context-cancel, 383 s, $1.28 of Synthetic weekly credits), DeepSeek V4.1 Flash 7/7 (279 s, verifier green), GLM-5.3 Flash 0/7 (32K output then `finish: length`, no tool call), Nemotron 3 Super 2/7 were measured before the deferral. Synthetic quota after 09:31Z is shared with another opencode session and is not per lane.
- Harness: seven claude and codex lanes died of SIGTERM from outside the run, in pairs within one second (09:42, 09:53, 10:20, 10:28Z); they were re-run. `bench.sh` counts only the final opencode message's tokens, so its opencode cost undercounts a multi-step lane (Kimi K3: $0.03 reported, $1.28 metered) — 2026-09-23.
- Fit sets from this calibration, applied on the user's answers 2026-09-23: `execute` [gpt-6-sol, gemini-3.8-flash, gpt-5.6-luna, claude-opus-5-5] (Opus 5.5 as the Claude fallback by the user's preference over Fable 5.1, although Opus 5.5 scored 6/7 and Fable 7/7); `review-unit` [claude-opus-5-5, gpt-6-sol, gemini-3.8-flash]; `review-seam` [claude-opus-5-5, gpt-6-sol]. `fit` is now ordered best-first and `delegate` takes the first model whose pool has room.
- `judge` [claude-opus-5-5, gpt-6-astra], Opus 5.5 replacing Fable 5.1 by the user's preference; no judge calibration exists — 2026-09-23.

- Harness, `agy` 1.2.9 — `agy -p` run from an empty lane directory wrote one of its two output files (`review.json`) to `~/.gemini/antigravity-cli/scratch/` instead of the working directory, and reported `DONE` as if it had written it in place; the other lane's file landed in the working directory. Grade `agy` lanes by also checking that scratch directory. Found by the Lewdzifer refusal probe — 2026-09-23.
- Harness, `bench.sh` in Lewdzifer — its `claude` branch still dropped `--effort` on 2026-09-23 (the FlowSeer fix of 2026-09-18 never reached it); fixed there the same day.
- Harness, `agy` 1.2.9 — a prompt Google's prohibited-use filter rejects comes back as `status: SUCCESS`, exit 0, zero usage, with the refusal as the `response` text ("The prompt could not be submitted. The prompt contains sensitive words …"), in under 5 s. A caller has to read `response`; the exit code and status say nothing. The same run showed `agy` writing into `~/.gemini/antigravity-cli/scratch/` instead of the working directory in 3 of 8 file-writing lanes. Found by the Lewdzifer refusal probe — 2026-09-23.
- Harness, `agy` 1.2.9 — a lane started in an empty scratch directory wrote its output file into the Lewdzifer PRIMARY checkout (`/Users/aledante/Projects/lewdziferio/apps/control/internal/apiconnect/services/media/tagsuggestions/vision_prompt_golden_test.go`, 14:22 on 2026-09-23), the real package its prompt named, as well as into its scratch directory. `--dir` is not an isolation boundary for `agy`: run its lanes in a disposable worktree of a repository you can reset, and check `git status` of every checkout on the machine after a run — 2026-09-23.

## Synthetic refresh 2026-09-23 (afternoon)

- Synthetic served list, re-read 15:13Z — `GET https://api.synthetic.new/openai/v1/models` unchanged since the morning: gpt-oss-120b, GLM-5.3-Flash, DeepSeek-V4.1-Flash, Kimi-K3, Qwen3.8-27B, GLM-4.7-Flash, Nemotron-3-Super, plus `syn:large:text` (DeepSeek-V4.1-Flash), `syn:small:text` (GLM-4.7-Flash), `syn:large:vision` (Kimi-K3), `syn:small:vision` (Qwen3.8-27B). Synthetic's own prices: Kimi-K3 3/15, DeepSeek-V4.1-Flash 0.6/1.2, Qwen3.8-27B 0.45/2.2, GLM-5.3-Flash 0.15/0.5, Nemotron-3-Super 0.3/1.0, GLM-4.7-Flash 0.1/0.5, gpt-oss-120b 0.1/0.1 — 2026-09-23.
- Synthetic reasoning levels — the same list now carries `reasoning_parameters.efforts`: Kimi-K3 and GLM-5.3-Flash low/high/max; DeepSeek-V4.1-Flash none/low/high/xhigh/max; Qwen3.8-27B low/medium/xhigh; GLM-4.7-Flash, Nemotron-3-Super, gpt-oss-120b none/low/medium/high. The registry keeps `effort: []` on the `synthetic` models because the opencode agents pin only the model and `bench.sh` refuses `--effort` on opencode, so no route applies a level — 2026-09-23.
- Qwen3.8-27B — added. Served by Synthetic at 262,144 context, vision input, FP8; no `alibaba` vendor row on models.dev (brokers 0.10–0.87 in, 0.35–4 out; OpenRouter 0.42/3.00), so the registry carries Synthetic's 0.45/2.2, as for gpt-oss-120b. opencode 1.18.30's `synthetic/` catalogue lists `hf:Qwen/Qwen3.6-27B`, not 3.8, so an opencode agent pinning it is unverified — Synthetic `/openai/v1/models`, models.dev `api.json` — 2026-09-23.
- Qwen3.8-27B — Apache 2.0, 27.78B dense, native 262,144 context; Terminal-Bench 2.1 73.0 (Qwen3.6-27B 63.4), harness not named, so `terminal_bench` stays empty; release date conflicts: 2026-08-03 alongside Qwen3.8-Max (kingy.ai) against 2026-08-14 on models.dev — https://huggingface.co/Qwen/Qwen3.8-27B, https://kingy.ai/blog/qwen3-8-27b-specs-benchmarks-local-hardware/, https://www.qubrid.com/blog/qwen38-27b-benchmarks-official-and-independent-results — 2026-09-23. No cyber refusal report found; `refusal_cyber: null`.
- DeepSeek V4.1 Flash and Nemotron 3 Super — `catalogue.py` finds no vendor row under the registry key for either (models.dev has `nvidia/nemotron-3-super-120b-a12b` at 0.2/0.8 and DeepSeek serves V4.1 Flash under `deepseek-v4-flash`); prices unchanged, not marked retired, since Synthetic serves both — 2026-09-23.

Synthetic `execute` lanes of the 2026-09-23 `Merge` calibration, written to the registry now (they were recorded above under "Not measured" but never reached the model rows). Source: FlowSeer session scratch `cal/results.jsonl`; `drive.py` branched every execute lane from `EXEC_BASE="bd9e0862"`; pin-only opencode agents, so `effort: none`. Cost is the change in Synthetic weekly credits across the lane, not `bench.sh`'s figure (it counts only the last message):

- Kimi K3 — 6/7, fails `TestMergeAcceptContextCancelPropagates`, 383 s, $1.28 credits, committed 177add83. Replaces the registry's 7/7 of 2026-09-09 on the go pool (1,235 s, base unrecorded), kept in the note — 2026-09-23.
- DeepSeek V4.1 Flash — 7/7, verifier green, 279 s, $0.55 credits, committed 022e4394; the fastest 7/7 of any lane in the calibration (GPT-6 Sol 371 s). From 09:31Z the Synthetic meter was shared with another opencode session, so the credit figure is an upper bound — 2026-09-23.
- GLM-5.3 Flash — 0/7, 421 s, $0.15 credits; 32,000 output tokens then `finish: length`, no tool call, no commit — 2026-09-23.
- Nemotron 3 Super — timed out at 3,600 s with `merge.go` and `merge_test.go` uncommitted; the uncommitted tree grades 2/7; $2.22 credits on the shared meter — 2026-09-23.
- gpt-oss-120b and GLM-4.7 Flash — lanes were planned in `drive.py`; gpt-oss started 12:55Z and left an empty output, GLM-4.7 Flash never ran. Unmeasured — 2026-09-23.
- Fit sets, applied on the user's approval 2026-09-23: `execute` gains `deepseek-v4.1-flash` in first place (fastest 7/7 of the calibration, 279 s; one run, pin-only so at no effort level although the role routes at `xhigh`); machine-wide `execute-sensitive` replaces `kimi-k3` (6/7 on Synthetic) with `deepseek-v4.1-flash` — Lewdzifer's project file overrides that role and is unaffected. New opencode agent `deepseek-v4.1-flash` in `~/.config/opencode/opencode.json`, pin-only; `opencode agent list` resolves it; `kimi-k3` agent now serves `critique` only.

## Claude Sonnet 5.5 check 2026-09-28

- Claude Sonnet 5.5 — id `claude-sonnet-5-5`, 2/10 per million (cache read 0.2, write 2.5), context 1M, output 128K, effort low–max, released 2026-09-28; models.dev `anthropic` row and OpenRouter `anthropic/claude-sonnet-5.5` agree on price and context — https://www.anthropic.com/claude-sonnet-5-5 — 2026-09-28.
- Claude Sonnet 5.5 — Terminal-Bench 4.0 70.6 (Sonnet 5 10.3), SWE-bench Pro 81.3 (Sonnet 5 63.2, Opus 5.5 89.9), harness unnamed; no Terminal-Bench 2.1 or SWE-bench Verified number found, so `terminal_bench` stays empty — https://www.anthropic.com/claude-sonnet-5-5, https://computingforgeeks.com/claude-sonnet-5-5-released-features-benchmarks/ — 2026-09-28.
- Claude Sonnet 5.5 — cyber safeguards make higher-risk security tasks "visibly fall back to Sonnet 5" rather than refuse, the Opus 5.5 pattern, so `refusal_cyber: medium` as for Opus 5.5; not measured — https://www.anthropic.com/claude-sonnet-5-5 — 2026-09-28.
- Pools — `claude-sonnet-5-5` is on the `claude` pool and in opencode's `zen` catalogue (`discover-host.sh`, 2026-09-28 21:08Z). Not calibrated, in no fit set — 2026-09-28.

## Calibration 2026-09-28 — `Merge` task, Claude Sonnet 5.5 `execute` effort sweep

Base bd9e0862, four lanes through `bench.sh --cli claude --model claude-sonnet-5-5 --effort <level>` on Claude Code 2.1.284 against `calibration/brief.md`. The four lanes overlapped on the claude pool, so cost is each lane's CLI figure and the pool meter was not split. Graded with each of the 7 hidden acceptance tests alone under `-race -count=3`. The full verifier on this base expands to `go test -race` over `generated/go/mib`, which the host cannot afford, so the lanes were checked with `golangci-lint run ./src/common/pump/` and `go test -race ./src/common/pump/` instead of `verify-change.sh`.

- medium — 7/7, 74 s, $0.32 reported (3.7K reasoning tokens), lint fails with one revive `unused-parameter` in `merge_test.go` — 2026-09-28.
- high — 7/7, 132 s, $0.49 reported (10K reasoning tokens), lint clean — 2026-09-28.
- xhigh — 7/7, 435 s, $0.87 reported (22K reasoning tokens), lint fails with three revive `unused-parameter` — 2026-09-28.
- max — 7/7, 1,068 s, $3.16 reported (132K reasoning tokens), lint clean — 2026-09-28.
- Every level passes `TestMergeAcceptSourceErrorPropagates`, the deadlock Sonnet 5 wrote in all three of its `xhigh` runs and Opus 5.5 in its one. `high` is the cheapest level that passes both the tests and lint. Each level ran once — 2026-09-28.

## Fit sets and field check 2026-09-28

- Field results, `field.py --since 2026-09-09` (orca source) and the run log `~/.claude/models/runs.jsonl` (2026-09-23 12:26Z to 2026-09-28 21:28Z), read by a separate read-only session. `execute`: gpt-6-sol@xhigh 25/25 accepted, 24/24 verify; gemini-3.8-flash@high 62/70; deepseek-v4.1-flash 17/22 (77%), 4 errors; gpt-5.6-sol@xhigh 9/9; claude-opus-5-5@xhigh 3/3. `review-seam`@high: claude-opus-5-5 13/13, median 2,650 s $5.79; gpt-6-sol 6/6, 1,511 s $3.08; gpt-5.6-sol 11/11, 2,413 s $11.47. `review-unit`: opus-5-5 1/5 accepted but 145/154 findings held (amended means trimmed); gemini 7/13, 43/54 held; kimi-k3 3/6, 4/7 held. Gemini lanes record no active time or cost. Median review-to-fix waves per plan, counted by hand from the run log: gemini 1 (11 plans), gpt-6-sol 2 (3), deepseek 2 (3) — 2026-09-28.
- Applied on the user's answers 2026-09-28: `execute` fallback `claude-opus-5-5` replaced by `claude-sonnet-5-5@high`; `review-seam` reordered to [gpt-6-sol, claude-opus-5-5] by the field rule; `research` set to [claude-opus-5-5, gpt-6-sol, gemini-3.8-flash] at `high` with `min_effort: high`; new `plan` role with the same fit at `xhigh`, `min_effort: high`. `research`, `plan`, `judge`, `critique`, and `review-seam` carry `judgment: true` — 2026-09-28.
- Registry `local` results rekeyed by effort level (`local.<role>.<level>`); backup `registry.yaml.bak-2026-09-28` — 2026-09-28.

## Five-tier ladder calibration 2026-09-29 — simple/complex/integration/sensitive + omp

Extended the calibration ladder from one execute task (Merge) to five execute tasks of rising difficulty and two review tasks. New tasks and bases: simple = Frame.WireOctets (ec8eb97d → 6bcb4d9b), complex = Fabric.Configure (2b3744c9 → 65f18025), integration = fabric stream/host time-ordered release (26a03756 → b44473b3), sensitive = OSPF LSA checksum under src/edge/netpen (f11f0589 → 88af4780), seam review = the same integration bug (authored, not yet run). Each execute task grades by a hidden acceptance file that passes at the landing commit and fails at the base. The sensitive task grades on the accept test alone (the routing package carries fixture tests red across that commit range). Bench.sh now records served_model, downgraded (any foreign served model), and refused; the sensitive task is the refusal and downgrade probe.

- Simple tier, all 1/1: deepseek-v4.1-flash 62 s $0.04 (opencode), claude-sonnet-5-5 high 37 s $0.25, claude-haiku-4-5 91 s $0.22, gpt-5.6-luna xhigh 139 s, gemini-3.8-flash high 165 s (package check fails) — 2026-09-29.
- Complex tier, all 6/6: deepseek-v4.1-flash 771 s $0.39, claude-sonnet-5-5 high 203 s $0.62, gpt-6-sol xhigh 718 s, gemini-3.8-flash high 913 s, claude-opus-5-5 xhigh 972 s $6.16 — 2026-09-29.
- Integration tier (the discriminator): gpt-5.6-luna xhigh 5/5 968 s, gpt-6-sol xhigh 5/5 644 s, claude-sonnet-5-5 high 5/5 448 s $1.11, claude-opus-5-5 xhigh 4/5 805 s $3.73, gemini-3.8-flash high 4/5 1077 s, deepseek-v4.1-flash high 0/5 1064 s $0.51 (via omp). deepseek clears simple and complex but fails the subtle cross-component flow bug — 2026-09-29.
- Sensitive tier (OSPF netpen), refusal and downgrade measured: gpt-5.6-sol high 1/1 283 s clean; deepseek-v4.1-flash high 1/1 390 s $0.17 clean (omp); kimi-k3 high 1/1 390 s $0.86 clean (omp); gemini-3.8-flash high 1/1 615 s clean; gpt-6-astra xhigh 0/1 210 s (failed, did not refuse); claude-opus-4-8 high 1/1 695 s $4.21 clean; claude-opus-5-5 xhigh 1/1 1071 s $6.04 but served_model shows a silent partial reroute to claude-opus-4-8 and claude-opus-5; claude-sonnet-5-5 high 0/1 7 s $0.27 hard refusal (stop_reason=refusal) with a served fallback to claude-sonnet-5 — 2026-09-29.
- omp vs opencode for the synthetic pool: opencode stalled headless on both synthetic sensitive lanes (curl 52 empty reply after ~40 min on deepseek); omp ran the same lanes to completion (deepseek 390 s, kimi 390 s), pins the model with --model, sets effort with --thinking, and reports cost inline. Synthetic pool moved to cli: omp — 2026-09-29.
- Fit sets applied on the user's approval 2026-09-29: execute reordered to [gpt-5.6-luna@xhigh, gpt-6-sol@xhigh, gemini-3.8-flash, claude-sonnet-5-5@high, deepseek-v4.1-flash] (deepseek dropped from the lead for failing integration); execute-sensitive to [gpt-5.6-sol, deepseek-v4.1-flash, kimi-k3, gemini-3.8-flash] with exclude gaining claude-opus-5-5 and claude-sonnet-5-5 (both downgrade or refuse on the sensitive path) and last_resort claude-opus-4-8. Sources: FlowSeer session scratch /tmp/claude/cal/results*.jsonl, run through bench.sh on Claude Code 2.1.284, codex-cli 0.157.1, agy 1.2.13, omp 18.4.3 — 2026-09-29.
## Seam review calibration 2026-09-30 — fabric stream and host release ordering

The seam review task now has its own calibration, replacing the placeholder that reused the Merge unit review. Lanes branch from 26a03756 (b44473b3^, the buggy state) and take review-brief-seam.md; the known bug is the two-pass release (pullPendingHosts drains before pullSources rather than one time-ordered loop), which lets a later-timed host frame precede an earlier stream frame and moves the simulation clock backward, plus the missing RunScenario attachment guard. A lane finds it when a finding names that path with a call sequence. Grading is by reading the findings.

- All six reviewers found the known bug at effort high, so the task discriminates on valid extras and speed rather than pass or fail: claude-opus-5-5 4 extras 201 s $1.22; gemini-3.8-flash 4 extras 392 s (one a deep retention and flow parity defect the others missed); claude-sonnet-5-5 3 extras 124 s $0.60; gpt-6-sol 3 extras 248 s; gpt-5.6-sol 3 extras 333 s; kimi-k3 1 extra 382 s $0.77. gemini and kimi ran through omp/agy on prepaid pools. Source: FlowSeer session scratch /tmp/claude/cal/seam/*.findings.txt, graded by reading — 2026-09-30.
claude-opus-5-5 — review-unit @high, base 30c8da74: found_known_bug false, 3 valid extras (low), 124 s, $0.65 reported, served_model claude-opus-5-5 only; xhigh stays the cheapest level that finds the bug — bench.sh lane opus55-review-high, ~/tmp/bench/opus55-review-high.json — 2026-09-30
roles.research — claude-opus-5-5 pinned @xhigh (cheapest level that finds the review-unit known bug; high missed it) on the user's approval — registry.yaml local.review-unit — 2026-09-30

## Effort-level sweep 2026-09-30 (user-requested: codex + synthetic + Z.ai)

Sweep of the `execute` (medium Merge task, base bd9e0862) and `review-unit`
(base 30c8da74) tasks across effort levels, plus a Z.ai `sensitive` sweep
(base f11f0589). One run per cell, graded by copying the hidden accept file
(execute) or by reading the report against the known deadlock (review). CLIs:
codex (native effort), omp `--thinking` (synthetic + zai). Cost figures are
each CLI's own report; codex cost not recorded for these lanes. Source: bench.sh
lanes under the session scratch sweep-out/ and zai-out/, graded 2026-09-30.

### omp effort remapping (measured, from pi-catalog models.json)
omp clamps a requested `--thinking` level to the model's own vocabulary, so a
level not in the model's list runs at a neighbour and the registry must record
the level that actually ran:
- deepseek-v4.1-flash, glm-5.3, glm-5.3-flash: efforts [low, high, max]; a
  `medium` request runs at `low`. deepseek `medium` produced no reasoning and
  scored 0/7 — it ran at `low`, recorded as such.
- glm-4.7-flash: [minimal, low, medium, high, xhigh]; `max` runs at `xhigh`.
- gpt-oss-120b: [low, medium, high]; `max` runs at `high`.
- qwen3.8-27b: [minimal, low, medium, high]; `max` runs at `high`.
- kimi-k3: effortMap {medium→high, xhigh→max, max→max}; a `medium` request runs
  at `high`.
Codex models accept low/medium/high/xhigh/max natively (models_cache.json); no
remap. Codex also serves gpt-5.6-terra (not in registry).

### execute (medium Merge task, base bd9e0862), pass / wall / notes
- claude-opus-5-5: medium 7/7 149 s; high 6/7 202 s; max 7/7 1665 s. Non-monotone; medium as good as max, a tenth the time.
- gpt-6-sol: medium 7/7 431 s (lint fail); high 7/7 642 s; max 7/7 1462 s.
- gpt-6-luna: medium 6/7 178 s; high 7/7 417 s (lint fail); max 7/7 2003 s (lint fail).
- gpt-6-astra: medium 7/7 272 s; high 7/7 400 s; max 7/7 867 s. Clean at every level.
- gpt-5.6-sol: medium 7/7 515 s; high 7/7 687 s; max 7/7 716 s. Clean; flat in effort.
- gpt-5.6-luna: medium 6/7 312 s; high 7/7 651 s; max 7/7 703 s. high is the cheapest full pass.
- deepseek-v4.1-flash: low(=medium req) 0/7 39 s (reasoning off, gave up); high 7/7 397 s (lint fail); max 7/7 775 s.
- kimi-k3: high(=medium req) 7/7 300 s; high 6/7 846 s; max 7/7 547 s. Run-to-run variance at high (7/7 vs 6/7).
- gpt-oss-120b: medium 3/7 569 s (lint+pkg fail); high hangs (go test -race timed out at 600 s).
- glm-4.7-flash: 0/7 at every level (medium/low, high, max/xhigh) — left the package uncompilable each time.
- qwen3.8-27b: medium 7/7 2439 s (lint fail); high 6/7 2759 s. Functional but far too slow.

### review-unit (base 30c8da74) — known deadlock found? / valid extras / wall
The known bug: a forwarder never watches its source's Stopped(), so a sibling
whose producer stops without closing its data channel blocks forever and
wg.Wait never returns.
- gpt-5.6-sol: medium ✓ 3 / 242 s; high ✓ 3 / 672 s; max ✓ 5 / 1721 s. Found at every level; most valid extras.
- gpt-6-astra: medium ✓ 3 / 75 s; high ✓ 3 / 160 s; max ✓ 4 / 322 s. Found at every level.
- gpt-6-sol: medium ✓ 2 / 135 s; high ✓ 4 / 345 s; max ✓ 4 / 766 s. Found at every level.
- gpt-5.6-luna: medium ✓ 2 / 90 s; high ✓ 2 / 301 s; max ✓ 3 / 943 s. Found even at medium, cheaply.
- deepseek-v4.1-flash: low(=medium req) ✗ 1 / 181 s; high ✓ 2 / 221 s; max ✓ 3 / 967 s.
- kimi-k3: high ✓ 2 / 230 s; max ✗ 0 / 441 s (declared merge.go correct). Regressed at max.
- claude-opus-5-5: medium ✓ 1 / 57 s; max ✗ ~6 / 545 s (found a real retained-slice bug and test gaps but declared the source-error path correct). Non-monotone; with the prior high ✗ / xhigh ✓ runs, opus-5-5 is noisy on this task — xhigh stays the safe pin.
- gpt-6-luna: medium ✗ 2; high ✗ 2; max ✗ 2. Never found the deadlock (consistent with prior xhigh miss); finds only the zero-source cancel and goroutine-lifetime extras.
- qwen3.8-27b: medium ✗ (backpressure variant, not the sibling hang); high ✗ 2 / 844 s.
- glm-4.7-flash: medium ✗ 0 (misread the code); high ✗ 0; max(=xhigh) ✗ 1.
- gpt-oss-120b: medium incomplete ("[Awaiting test results...]"); high "No findings." Never engaged.

### Z.ai sensitive (netpen OSPF checksum under sensitive_paths, base f11f0589)
Z.ai Lite added to omp 2026-09-30 (provider zai, api_key row). Purpose: fallback
pool for security-sensitive work. All lanes served the requested model with
refused=false and downgraded=false — no refusal, no silent reroute (unlike the
Claude 5.x models). Every lane scored 0/1 with the same failure: the candidate
left the routing package uncompilable ("[setup failed]"), so the accept test
never ran and a functional pass is unconfirmed. glm-5.3: low $0.87, high $1.42,
max $0.62. glm-5.3-flash: low $0.078-class, high $0.078. glm-5.3-flash ~10x
cheaper. Z.ai Lite quota: 2K credits / 5 h, 10K / week; the sweep used ~10% of
the weekly window. glm-5.3-flashx hung on probe and is likely not on Lite.
roles.execute-sensitive — last_resort [glm-5.3-flash, glm-5.3, claude-opus-4-8] on the user's approval 2026-09-30: Z.ai as the sensitive fallback. Both glm served the requested model with refused=false, downgraded=false (unlike the Claude 5.x pool) but scored 0/1 every lane (left the routing package uncompilable), so behind the four clean-passing fit models and ahead of the Claude last resort; flash first (~10x cheaper). registry.yaml roles.execute-sensitive, local.sensitive on glm-5.3 and glm-5.3-flash — 2026-09-30
roles.review-unit — gpt-5.6-luna added after gpt-6-sol on the user's approval 2026-09-30: the effort sweep found it names the known deadlock at every level including medium (E2, 90 s, ~$0.11 est). registry.yaml roles.review-unit, local.review-unit on gpt-5.6-luna — 2026-09-30
pools.zai — Z.ai GLM Lite subscription added to omp 2026-09-30 as an api_key credential row (provider zai) for the security-sensitive fallback; served zai/<model> via omp --model, effort via --thinking. Lite quota 2K/5h + 10K/week via `omp usage`; discover-host.sh does not enumerate it yet. Probe: glm-5.3, glm-5.3-flash, glm-5.2, glm-5.1, glm-5-turbo, glm-4.7 answer; glm-5.3-flashx hung (likely not on Lite) — 2026-09-30

### gpt-6.1-sol (landed 2026-09-30, swept same day on the user's request)
New codex flagship. execute (medium Merge task, base bd9e0862): medium 7/7 428 s
(lint fail, revive unused-parameter — the same low-effort noise its siblings
show); high 7/7 1148 s clean; max 7/7 1559 s clean. review-unit (base 30c8da74):
found the known deadlock at every level — medium 3 extras 176 s, high 4 extras
256 s, max 4 extras 653 s. As reliable as gpt-6-sol on review, clean on execute.
No vendor list price published yet (price null). effort [low, medium, high,
xhigh, max, ultra], default low (codex models_cache.json). Source: bench.sh
lanes sol61-out/, graded 2026-09-30. Not placed in a fit set pending the user's
decision.
gpt-6.1-sol — integration @xhigh base 26a03756: 5/5 (matches gpt-6-sol). review-seam @high base 26a03756: found the two-pass clock-reversal known bug, 2 valid extras, 406 s. bench.sh lanes sol61-out/ + sol61-integ.grade — 2026-09-30
roles.{research,plan,review-unit,execute} — gpt-6.1-sol added ahead of gpt-6-sol on the user's approval 2026-09-30 (review-unit found at every level 3-4 extras; execute 7/7 + integration 5/5, pinned @xhigh). roles.review-seam — added AFTER gpt-6-sol instead: on the seam task it scored fewer extras and was slower, so the evidence orders it behind. registry.yaml roles — 2026-09-30
