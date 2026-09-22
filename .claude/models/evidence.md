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

## Catalogue pull 2026-09-19 19:50Z

models.dev `api.json` and OpenRouter `/api/v1/models`, read through
`tune/scripts/catalogue.py`. Anthropic, OpenAI and Google list prices are
unchanged since 2026-09-09; so are GLM-5.3, Kimi K3, Qwen3.8 Max, Qwen3.8
Flash, DeepSeek V4 Pro and Grok 4.6.

- models.dev carries a `synthetic` provider whose ten rows are exactly the ten `hf:` ids opencode lists, at the prices and contexts Synthetic serves rather than the upstream vendor's. Those rows are what opencode's own cost line matches: GPT-OSS-120B billed $0.0015 on ~15K tokens in the 2026-09-19 smoke test, which is Synthetic's 0.1/M and not NVIDIA's or OpenAI's free hosting. The registry takes price and context for every `pool: synthetic` model from that row — models.dev `api.json`, provider `synthetic` — 2026-09-19.
- Synthetic truncates context: Kimi K3 524,288 against the vendor's 1,048,576, GLM-5.3-Flash and DeepSeek V4.1 Flash 524,288 against 1,000,000, GLM-4.7-Flash 196,608 against 200,000 — same source — 2026-09-19.
- GLM-5.3-Flash — Z.ai list is 0.15/0.50 per million; the registry's 0.07/0.25 was the launch promotion, which ended at 24:00 on 2026-09-09 (UTC+8) — models.dev `zai` row and https://www.mindstudio.ai/blog/glm-5-3-flash-pricing-api — 2026-09-19.
- GLM-5.3-FlashX — 0.37/1.25 per million, 1M context, on Z.ai's API since 2026-09-18. Not a new model: GLM-5.3-Flash's weights (320B total, 18B active) on a faster serving stack at up to 200 output tokens/s, published with no evaluation of its own. Synthetic does not serve it — https://www.orcarouter.ai/blog/glm-5-3-flashx-release and https://apimaster.ai/blog/glm-5-3-flashx-api — 2026-09-19.
- DeepSeek V4.1 Flash — shipped 2026-09-10 as `deepseek-flash`; DeepSeek routes `deepseek-v4-pro` traffic to it from 04:00 UTC on 2026-09-14, so the V4 Pro id no longer names the weights the registry's 87.9 was measured on — https://www.mindstudio.ai/blog/deepseek-v4-1-flash-benchmarks — 2026-09-19.
- DeepSeek V4 Flash — DeepSeek's own row is now 0.15/0.60 per million at 1M context, not the 0.14/0.28 recorded on 2026-09-09; 0.14/0.28 survives as NVIDIA's hosted copy (`nvidia` provider, `deepseek-ai/deepseek-v4-flash`) — models.dev `api.json` — 2026-09-19.
- Vendor and broker still disagree by more than 20% on three models, and the registry keeps the vendor's: GPT-5.6 Sol 4/20 against OpenRouter 2/10, GLM-5.3 1.4/4.4 against 0.91/2.86, Kimi K3 3/15 against 1.70/8.50 — both feeds — 2026-09-19.
- On OpenRouter in the last 60 days and not in the registry, none with a pool this host can reach: `z-ai/glm-5.3-flashx` (added), `openai/gpt-6-astra-pro` (still no vendor row, still out), `qwen/qwen3.8-max-0902`, `deepseek/deepseek-v4-flash-vision-exp`, `qwen/qwen3.8-27b`, `google/gemini-3.7-flash`, `qwen/qwen3.8-2.4t-a95b`, `deepseek/deepseek-v4-pro-0813`, `nvidia/nemotron-3.5-lightning`, `deepseek/deepseek-v4-flash-0731`, `qwen/qwen3.7-flash` — 2026-09-19.

### Benchmarks for the three models Synthetic answers for

None of these fills the registry's `terminal_bench` column, which holds
Terminal-Bench 2.1 only, and none is a calibration result, so none of them
enters a fit set on these numbers.

- DeepSeek V4.1 Flash — Terminal-Bench 2.1 90.6 and Terminal-Bench 3.0 30, DeepSeek's own runs on the Minimal mode of the DeepSeek Harness at 1M context, a different harness from the 88.2/88.3/86.6 figures already in the registry; DeepSWE v1.1 74.2, not SWE-bench Verified — https://www.mindstudio.ai/blog/deepseek-v4-1-flash-benchmarks and https://huggingface.co/deepseek-ai/DeepSeek-V4.1-Flash — 2026-09-19.
- Nemotron 3 Super 120B A12B — SWE-bench Verified 60.5 on OpenHands, Terminal-Bench Core 2.0 31.0 on Harbor, NVIDIA's own runs. Terminal-Bench Core 2.0 is not Terminal-Bench 2.1 — https://research.nvidia.com/labs/nemotron/files/NVIDIA-Nemotron-3-Super-Technical-Report.pdf and https://openrouter.ai/nvidia/nemotron-3-super-120b-a12b/benchmarks — 2026-09-19.
- GPT-OSS-120B — around 62 on SWE-bench Verified in public reports, harness unnamed; OpenAI's model card gives Codeforces, SWE-bench and tau-bench without a Terminal-Bench figure. Its 131,072-token context on Synthetic is the smallest in the registry — https://arxiv.org/pdf/2508.10925 and https://artificialanalysis.ai/models/gpt-oss-120b — 2026-09-19.
- No refusal report found for any of the three; all are open-weight releases served without a vendor safety gateway, so `refusal_cyber: low` stands on the same reasoning as the other open-weight rows and not on a measurement — 2026-09-19.

## Host discovery 2026-09-19 19:49Z

- CLIs: claude 2.1.278, codex 0.153.4, agy 1.2.7, opencode 1.18.31. Orca reachable, `--model` pinnable for claude, codex and cursor — `discover-host.sh` — 2026-09-19.
- `google` is signed out: `agy -p /quota` prints an OAuth URL and exits with "authentication failed or timed out", and `agy models` answers "Please sign in to view available models". Until someone completes that login, Gemini 3.8 Flash is unreachable, and it is the only model in the `lookup` and `critique` fit sets besides one Claude and one Synthetic model — 2026-09-19.
- `pool-usage.sh` reports `claude: signed_in: false` while the `claude` CLI is signed in on this host. The row comes from `orca account list --json`, where `claude.accounts` is empty and `rateLimits.claude` is null, because Orca has no Claude account registered. The `codex` row escapes the same fate only through its `systemDefault.hasAuth` fallback; there is no equivalent for claude, although `~/.claude/.credentials.json` exists and `~/.claude.json` shows an active Max 20x account. `delegate` drops every model whose pool row reads `signed_in: false`, so this reading takes all four Claude models out of every fit set while the coordinating session is itself running on that pool — 2026-09-19.

## opencode agent block 2026-09-19

- `~/.config/opencode/opencode.json` defines nine agents, every one named after a model id: `claude-fable-5-1`, `claude-opus-5`, `claude-sonnet-5`, `claude-haiku-4-5`, `gpt-6-astra`, `gpt-5.6-sol`, `gemini-3.8-flash` (all pinned to `opencode/…`, the zen pool) and `kimi-k3`, `glm-5.3-flash` (pinned to `synthetic/…`). There is no `~/.config/opencode/agent/` directory, so that file is the whole set — 2026-09-19.
- None of the five agent names in the registry's `opencode_agents` (`research`, `execute-open`, `execute-sensitive`, `critique`, `overflow-frontier`) exists in that file, and neither does any of the ten per-model `agent:` names the registry carries (`execute-glmflash`, `execute-open`, `execute-flash`, `execute-nemotron`, `execute-gptoss`, `execute-kimicode`, `execute-qwen`, `execute-glm47`, `execute-glm52`, `execute-minimax`). `orca-worker.sh` passes `--agent` through to `opencode --agent` unchanged, so a synthetic or zen lane dispatched on today's registry launches an agent opencode does not have. The claim in 0d774ceb that the ten ids were "wired in ~/.config/opencode/opencode.json" does not hold — 2026-09-19.
- The registry also carries two incompatible pinning schemes at once: a per-model `agent:` field, which lets `delegate` choose the model and then name its agent, and the role-to-agent `opencode_agents` map, which fixes one model per role and is what `delegate`'s SKILL.md describes. Only one can decide a lane — 2026-09-19.
- The zen pool serves eight ids, all small or free: `big-pickle`, `jev-1.13-free`, `ling-3.0-flash-fin-free`, `mimo-v2.5-free`, `muse-spark-1.2-contributor-free`, `muse-spark-1.3-contributor-free`, `nemotron-3-ultra-free`, `nemotron-3.5-lightning-free`. The seven zen-pinned agents in opencode.json point at Claude, GPT and Gemini ids the pool no longer carries, so they are dead as well. The registry has no zen model rows, and `overflow: overflow-frontier` names a frontier overflow lane the pool cannot serve — `opencode models` 1.18.31 — 2026-09-19.

## opencode agent dispatch, probed 2026-09-19 20:00-20:40Z

The agent block was reconciled this run: `~/.config/opencode/opencode.json`
gained one agent per new synthetic model, named after the model id like the
nine already there, and the registry's `agent:` fields now name those instead
of the invented `execute-*` set. The probes below are the first time any agent
in that file has been exercised; the fit sets its descriptions claim were
never evidence that dispatch works.

Probe: one `opencode serve` per model in a fresh directory, a fresh session per
request, a hard `--max-time` on every call, one prompt — read `probe.txt` with
the file tool and reply with its text, which only a tool call can produce.

- `glm-5.3-flash` — pass through the default agent in 8 s and through the named agent in 6 s, so a named agent is not itself the problem — 2026-09-19.
- `nemotron-3-super` — pass through its named agent in 11 s — 2026-09-19.
- `deepseek-v4.1-flash` — pass through its named agent in 37 s — 2026-09-19.
- `gpt-oss-120b` — no reply through its named agent in 90 s and again in 180 s, with an assistant message recorded at zero input, zero output and zero cost in `~/.local/share/opencode/opencode.db`; the same model and prompt through the default agent passed in 7 s. Something about the agent definition (it differs from opencode's default `build` agent only by `mode: "all"` and a description) leaves this model producing nothing. `delegate` pins the synthetic pool by agent name and has no other path, so the model is unreachable for delegation and the registry gives it `pool: null` — 2026-09-19.
- Weekly Synthetic credit read $119.86 after the morning's ten probes and $114.01 before the afternoon's calibration lanes, a $5.85 drop the hung probes do not account for: opencode recorded zero tokens and zero cost for all of them. Synthetic counts usage from every host on the key, so the difference is not attributable from this machine, and no cause is claimed — 2026-09-19.

## Calibration attempt 2026-09-19 — blocked on the toolchain

Four lanes were started on base `bd9e0862` (`d4421211^`, so they measure
implementation): `glm-5.3-flash`, `kimi-k3`, `deepseek-v4.1-flash` and
`nemotron-3-super`, each through `bench.sh --cli opencode` with the model's
own agent. `gpt-oss-120b` was left out; it returns nothing through a named
agent.

- No Go toolchain exists on this host: `go` is absent from PATH, from `/usr/local/go`, from every version manager directory, and there is no `~/go` or `~/.cache/go-build`. `go.mod` requires 1.27. Grading a lane means running the seven hidden acceptance tests under `-race`, so **no lane can be graded here**, and a fit set cannot move: `tune` admits a model to a role on a calibration result and on nothing else. The same gap fails the repository Stop hook, whose conformance gates shell out to `go` — 2026-09-19.
- `bench.sh` reports success for a lane that never ran. The `kimik3` lane's `opencode serve` never bound its port (`curl: (7) Failed to connect`, empty serve log, most likely because four servers were started at once), the session was never created, and the script still wrote a result file with `"exit": 0`, `wall_s: 31` and zero usage. Nothing in that JSON distinguishes a model that did nothing from a lane that never started; only the empty `.raw` and the `.stdout` traceback do. `code=$?` there captures the `case` block, not the curl — 2026-09-19.
- Four `opencode serve` instances at once corrupt each other's work. They share one SQLite database at `~/.local/share/opencode/opencode.db`, and running the four lanes in parallel produced exactly the failures that implies: the `kimik3` server never bound its port and its lane never started, and the `dsv41flash` lane died 202 s in with `{"name":"UnknownError"}` to the client and `level=ERROR message=process error="Failed to execute statement"` in `~/.local/share/opencode/log/opencode.log`. Every single-server probe this session succeeded, five for five. Calibration lanes on the opencode CLI must run one at a time; `calibration.md` says one worktree per lane and does not say that — 2026-09-19.
- DeepSeek V4.1 Flash had written a 105-line `merge.go` with a full contract doc comment when the database error killed its lane, and no `merge_test.go`, which the brief requires. The lane is void, not a result — 2026-09-19.

## Fixes researched and applied 2026-09-19

- `OPENCODE_DB` moves opencode's SQLite database and nothing else: `OPENCODE_DB=/tmp/oc-lane1.db opencode db path` prints that path, and `opencode auth list` under the same variable still reads `~/.local/share/opencode/auth.json` and still shows Synthetic signed in. `XDG_DATA_HOME` also moves the database but takes `auth.json` with it, and `OPENCODE_DATA` does nothing. `bench.sh` now exports `OPENCODE_DB="$raw.db"` per lane. This addresses the shared-database failure that voided two lanes; four servers at once has not been re-tested since — 2026-09-19.
- `bench.sh` now reports what a lane actually did. Replaying this run's raw output through the patched parser turns the three misleading result files into: `glm53flash` `finish: length` with `tool_calls: 0`; `dsv41flash` `error: UnknownError: Unexpected server error`; `kimik3` an `.err` naming the server that never accepted a session, with a non-zero exit instead of 0. `finish` and `tool_calls` matter because a model that reasons to its output cap and never calls a tool spends a normal-looking number of tokens — 2026-09-19.
- `pool-usage.sh` now falls back to the `claude` CLI's own OAuth token (`~/.claude/.credentials.json`, `claudeAiOauth`, valid when either `expiresAt` or `refreshTokenExpiresAt` is in the future) when Orca has no registered Claude account, the way `codex` has always fallen back to `systemDefault.hasAuth`. The row reads `signed_in: true` with `windows: null` and a note saying Orca has no account for it, so `delegate` treats it as signed in with unknown headroom rather than dropping every Claude model — 2026-09-19.
- `gpt-oss-120b` through its named agent is still unexplained. The agent is registered (`opencode agent list` shows `gpt-oss-120b (all)`), and the same `mode: all`, no-`prompt` shape works for `glm-5.3-flash`, `nemotron-3-super` and `deepseek-v4.1-flash`, so the shape alone is not the cause; the model is the smallest of the five. The untried discriminating test is a second agent for the same model with `mode: primary` and an explicit `prompt`, probed the same way. It was not run: a probe needs its own `opencode serve`, and a calibration lane was in flight on the shared database — 2026-09-19.

## opencode caps output at 32,000 tokens, 2026-09-19

Both GLM lanes that "failed" this way were cut off by the harness, not by the
model, and one of them is the basis of a fit-set removal.

- The `glm53flash` lane ended `finish: length` at exactly 32,000 output tokens with 138,725 characters of reasoning and no tool call. The reasoning is coherent throughout and ends mid-token while writing an acceptance test, having just worked out that the merged order assertion has to be per-source subsequence order plus total counts rather than global positions. It is a model working the problem, not looping — 2026-09-19.
- The cap is opencode's, established by elimination rather than by reading its code. Synthetic itself does not cap at 32,000: the same model called directly at `POST api.synthetic.new/v1/chat/completions` with `max_tokens: 60000` returned 33,100 completion tokens and `finish_reason: stop`. opencode's own catalogue does not cap it either: `GET /config/providers` on a running server reports `limit.output` 65,536 for `hf:zai-org/GLM-5.3-Flash`. Yet the turn through opencode stopped at exactly 32,000. So opencode sends a smaller ceiling than the limit it publishes. The binary carries `var M7=32000`, but the neighbouring `maxOutputTokens:32000` entries belong to an Anthropic table (`claude-opus-4-1`) and the unknown-model fallback there is 4096, so that constant is a candidate and not a proven mechanism — measured 2026-09-19.
- This voids the 2026-09-09 GLM-5.3 `execute` result as well: "32K reasoning tokens then `finish: length` with no tool call" is the same signature at the same cap. GLM-5.3 left the `execute` fit set on that lane, so the removal rests on a harness artifact and the question is unmeasured rather than settled. Kimi K3's 7/7 is unaffected — it finished inside the cap — but any ranking of a reasoning-heavy model against a terse one on these lanes is biased by it until the cap is raised — 2026-09-19.
- The message POST body cannot raise it: the server's OpenAPI at `/doc` gives `UserMessage` only `agent`, `format`, `id`, `model`, `role`, `sessionID`, `summary`, `system` and `tools`, with no token field. The remaining lever is per-model config: `ProviderConfig.models.<id>.limit` takes `{context, output}`, so `~/.config/opencode/opencode.json` can override what opencode holds for a model. That override is set for GLM-5.3-Flash and is being tested by re-running its lane — 2026-09-19.
- A short prompt does not test the cap. "Print the integers from 1 to 9000" answers in 179 tokens with `finish: stop` through opencode's agent, though the same prompt on the raw API produced 33,100: the agent's system prompt talks the model out of it. Only a real brief reproduces the ceiling — 2026-09-19.

## The agent profiles were the regression, 2026-09-19

- Every synthetic model answers on opencode's default agent with the model in the request: all five ids in 8-19 s this morning, Kimi K3 7/7 on 2026-09-09 (no profile the registry named then existed in `opencode.json`), and gpt-oss-120b in 7 s again this afternoon. The failures all came through a custom agent profile: gpt-oss-120b returned nothing twice, and GLM-5.3-Flash reasoned to 32,000 tokens without a tool call. A profile that only pins a model carries no `prompt`, so the model gets tools and none of the instructions that make the default agent act — 2026-09-19.
- The profiles were never needed. `opencode` and `opencode run` both take `-m, --model provider/model` on the launch line; `orca-worker.sh` had refused `--model` for opencode on the belief that "the agent fixes the model". It now launches `opencode --model <pool_id>` on the default agent, the registry carries no `agent` fields, and `~/.config/opencode/opencode.json` is back to a bare `$schema`. The seven zen-pinned profiles that went with it named ids the zen pool no longer lists — `opencode --help`, `opencode run --help` 1.18.31 — 2026-09-19.
- Whether the 32,000-token ceiling is also the profile's doing is being measured: the same brief on GLM-5.3-Flash with no profile is in flight, beside default-agent lanes for Kimi K3, DeepSeek V4.1 Flash and gpt-oss-120b — 2026-09-19.

## Calibration 2026-09-19 — `Merge` task on the default agent, base `bd9e0862`

Graded with each of the seven hidden acceptance tests run on its own under
`-count=3`, **without `-race`**: this host has no C compiler (`cc`, `gcc`,
`clang` all absent) and `go test -race` refuses to start without cgo. The
`synctest` bubbles still fail on a leaked or deadlocked goroutine, so a
pass here covers ordering, completion, stop and cancel propagation, and
goroutine hygiene, and does not cover data races. A `race: false` field on
each `local` result says so. Installing gcc is what a race-checked
re-grade needs; the worktrees stay until then.

- gpt-oss-120b — 6/7 in 282 s, $0.004 reported, `finish: stop`. Wrote a 99-line `merge.go` and a 175-line `merge_test.go`; its own tests pass. Fails `TestMergeAcceptContextCancelPropagates` only. Same model, same brief, returned nothing through a named agent profile twice this afternoon — 2026-09-19.
- `bench.sh`'s `tool_calls` counts the parts of the final assistant message only, so a lane whose last turn is a text summary reports `tool_calls: 0` after having called tools for minutes; the worktree diff is the record of what a lane did, and that field only distinguishes "never acted" from "acted" when the turn ended `finish: length` — 2026-09-19.
- GLM-5.3-Flash, default agent, no profile — `finish: length` at exactly 32,000 output tokens, no tool call, no file, 775 s, $0.017. Third run at the same wall: through a profile (469 s), through the profile with `provider.synthetic.models.<id>.limit.output: 65536` set (423 s), and now on the default agent. The profile is therefore not what caps GLM; opencode is, and this model reasons past 32,000 on this brief on any agent. The gpt-oss-120b contrast stands: it went from nothing through a profile to 6/7 without one — 2026-09-19.
- Nemotron 3 Super's lane record is void for a harness reason of my own making: bash reads a script incrementally, and `bench.sh` was patched on disk while the pre-patch process was inside its 3,600 s `curl`. When the curl gave up the old process resumed at a byte offset in the new file, wrote "opencode serve never accepted a session" to its `.err`, produced no JSON, and exited 2. The model had worked the whole hour (its two files are staged in the worktree) and the reply never arrived before the ceiling. Never edit `bench.sh` while a lane is running it; the worktree diff is what remains — 2026-09-19.
- Nemotron 3 Super — 1/7 after the full 3,600 s the harness allows, cut off mid-turn (default agent through a profile, since it started before the profiles went; profiles did not stop it acting, it wrote and rewrote both files for the hour). Its `merge.go` had shrunk from 83 lines to 61 by the end; the package compiles and passes `TestMergeAcceptZeroSourcesCompletes` only. Cost is unrecorded, see the lane-record note above — 2026-09-19.
- Kimi K3 and DeepSeek V4.1 Flash, default agent — both killed by the OS at 17-18 messages in, with both files on disk, when the host ran low on memory during a concurrent grade of Nemotron's own 328-line test file. Mid-turn state, so both are void and re-run. Lanes and grades do not overlap from here on — 2026-09-19.
- Kimi K3, default agent on Synthetic — 6/7 in 460 s, $0.026 reported, `finish: stop`. Wrote an 86-line `merge.go` and a 250-line `merge_test.go`; compiles, its own tests pass. Fails `TestMergeAcceptSourceErrorPropagates` only, the same source-error deadlock Sonnet 5 failed on 2026-09-09 and 2026-09-18. Its 7/7 on 2026-09-09 was the Go pool at the vendor's 1M context under `-race` in 1,235 s; the two runs differ in pool, context, race detector and base, so this is a second sample and not a regression claim — 2026-09-19.
- Claude Code's background-task monitor killed three lanes and a grade for "running low on memory" while `free` reported over 30 GB available; each kill coincided with Go build activity (a grade; DeepSeek V4.1 Flash running `go run mvdan.cc/gofumpt` inside its lane), which fills the page cache, and `free` showed under 1 GB free with 40 GB in cache at the time. The monitor appears to read free rather than available memory. Long opencode lanes therefore run detached from the harness (`setsid nohup`, a done-marker file, polled), under `systemd-run --user --scope -p MemoryMax=16G` where a user manager exists and with `GOFLAGS=-timeout=120s` so a candidate's own deadlocking test cannot run away — 2026-09-20.
- DeepSeek V4.1 Flash, default agent on Synthetic — 7/7 in 332 s, $0.002 reported, `finish: stop`, on the third attempt (the first two were killed by the shared-database bug and by the harness's memory monitor, not by the model). Wrote a 122-line `merge.go` and a 268-line `merge_test.go`; compiles, its own tests pass — 2026-09-20.

Scoreboard for the `Merge` task on base `bd9e0862`, default agent, graded
without `-race`, one run each unless noted:

| Model | Pass | Wall | Cost |
| --- | --- | --- | --- |
| deepseek-v4.1-flash | 7/7 | 332 s | $0.002 |
| kimi-k3 | 6/7 (7/7 on 2026-09-09 with `-race`, Go pool, 1M context) | 460 s | $0.026 |
| gpt-oss-120b | 6/7 | 282 s | $0.004 |
| nemotron-3-super | 1/7, cut off at the 3,600 s ceiling | 3,600 s | unrecorded |
| glm-5.3-flash | void ×3, opencode's 32,000-token ceiling | 423–775 s | $0.017–0.019 |

No fit set moved on these. Two changes are worth asking for: `deepseek-v4.1-flash`
into `execute` on the only 7/7 of the day at a hundredth of Kimi's cost, and
`kimi-k3`'s place there reconsidered once a race-checked run exists. The five
bench worktrees under `~/Projects/worktrees/FlowSeer/` stay for that re-grade.
- `execute` fit set — `deepseek-v4.1-flash` added on the person's decision, on its 7/7 of 2026-09-20: one run, default agent, base `bd9e0862`, graded without `-race`. A race-checked run on the kept worktree is the follow-up that either confirms or reverses it — 2026-09-22.
