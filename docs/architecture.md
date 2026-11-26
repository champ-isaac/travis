# Local LLM architecture

This is the refactored design for the local-LLM part of Travis. It comes from a review of the first sketch (chat UI, Go gateway with auth, sessions and rate limiting, MLX-VLM server) against the code on `develop` and the project README. An adversarial review then ran the design against a live `mlx_lm.server` with Inkling 2 Mini and the real Go code. This version fixes the nine findings it verified. It is a design only. No code follows it yet.

The README says the POC tests whether a local AI works together with cloud AI, using the cloud AI to verify the local AI's results. The design is built around that.

## Architecture

The diagram is in [architecture.mmd](architecture.mmd). Open it in VS Code with the Mermaid extension to preview it.

In short, a pure Go core holds the run logic, the router, the tool loop, the tool registry and the run transcripts. The Travis CLI calls the core directly. The core reaches models only through the router. The router reads a registry file and builds a provider stack for each endpoint. The stack is a usage recorder, then an in-flight limiter, then a provider. No path to a provider skips the recorder, so every call, from `run` or from `agent`, adds a line to the usage ledger. The local provider points at `mlx_lm.server` on port 8080 and owns a decoder for Inkling's output format. The cloud provider points at a cloud verifier and reads its key from the environment. An HTTP gateway (`cmd/gatewayd`) is a thin optional shell for later, added only when a non-Go client needs an OpenAI-compatible endpoint.

Facts about the local server that the design relies on. All were measured on this Mac unless marked otherwise.

- It serves `/v1/chat/completions`, `/v1/completions`, `/v1/models` and `/health`. `/v1/responses` returned 404 for one adversary, which I did not rerun.
- It binds to 127.0.0.1 by default and its source has no auth code. A request with a wrong `Authorization` header got HTTP 200 for two adversaries, which I did not rerun. Its own log says it is not for production.
- It has flags for decode and prompt concurrency, prompt-cache size, KV-cache quantization and a draft model. Concurrency was not tested.
- It takes the model from each request's `model` field. `/v1/models` reports the model's file path as its id. A request that names anything else than that path or `default_model` makes the server drop the loaded weights and look the name up on huggingface.co. The next normal request reloaded the weights in 10.3 s, against 0.6 s warm. So "one model per process" holds only when every request sends the exact id.
- For Inkling 2 Mini it returns no `tool_calls` (`has_tool_calling False`, `tool_parser None`). Tool calls and thinking are raw text in `content`, wrapped in control tokens. A tool call looks like `<|message_model|><|content_thinking|>…<|end_message|><|message_model|>calculator<|content_invoke_tool_json|>{"name":"calculator","args":{…}}<|end_message|>`, with the arguments under the key `args`. A final answer looks like `<|message_model|><|content_text|>…<|end_message|>`.
- Non-streamed replies include `usage`. Streamed replies include it only when the request sets `stream_options.include_usage`, and then in a final chunk with an empty `choices` list.
- A streamed reply equals the non-streamed reply at temperature 0 and differs at temperature 0.7.
- Two adversaries saw the chat template accept an assistant message with `tool_calls` followed by a `tool` message with `tool_call_id`, and then answer. They did not rerun it for this document, and step 7 checks it.
- Inkling 2 Mini loads in `mlx_lm.server`. It did not load in mlx-vlm, which rejected the checkpoint with 1,559 unmatched weight names. The design does not use mlx-vlm.

## Problems found

Rows 1 to 8 come from the first review. Rows 9 to 13 come from the adversarial review. The last column says which build step resolves each row.

| # | Problem | Evidence | Change | Step |
|---|---|---|---|---|
| 1 | The engine is named MLX-VLM | mlx-vlm failed to load the Inkling 2 Mini checkpoint (1,559 unmatched weight names) | Call the layer "inference server", currently `mlx_lm.server`, and keep it behind the `Provider` interface. | 1 |
| 2 | The cloud verifier is missing | The README says cloud AI verifies local AI | Add a cloud provider and a verification step as the core of the design. | 9, 10 |
| 3 | Auth and rate limiting | The server binds to loopback and there is a single user | Skip them for now. Limit in-flight requests per endpoint instead, because the GPU is the real limit. | 4 |
| 4 | Server-side sessions | The chat API is stateless, so the caller sends the history | The caller owns the conversation. Keep one transcript per run for audit, not sessions. | 8 |
| 5 | Batching in the gateway | The engine has its own concurrency flags (not benchmarked) | Do not batch in the gateway. Cap in-flight requests per endpoint and queue the rest. The limit stays at 1 until the engine's concurrency is measured. | 4 |
| 6 | Streaming uses the Responses API | `AskStreaming` in `internal/openai` calls `/v1/responses`, which returned 404 for one adversary | Use chat-completions streaming. | 5 |
| 7 | "Multiple models later", and the `model` field can evict the weights | One model fits at a time (Inkling 2 Mini peaked at 17.4 GB on a 64 GB Mac). A request with `model: "inkling-2-mini"` returned 404 after a huggingface.co lookup, and the next request took 10.3 s to reload the weights. | The registry maps each logical name to an endpoint and the exact server model id. The router and provider never send any other string. Startup refuses a registry id the endpoint does not report. | 3 |
| 8 | Monitoring comes later, and streamed replies carry no token counts by default | Streamed replies have no `usage` unless the request sets `stream_options.include_usage` (0 usage chunks without it, one final chunk with it). README question 2 asks whether the hybrid setup costs more. | Every provider call goes through a usage recorder. The provider always sends `stream_options.include_usage=true`. | 5, 6 |
| 9 | The server does not parse Inkling's output | No `tool_calls` for Inkling 2 Mini. Replies carry control tokens and inline thinking in `content`. | The local provider owns a decoder. It strips control tokens, splits thinking from text and parses tool calls into IDs and arguments. The core only sees clean types. | 2, 7 |
| 10 | The local client needs an API key | `New` in `internal/openai/client.go` returns nil when the key is empty, and `cmd/main.go` then dereferences it. The server needs no key. `.env` has `BASE_URL` pointing at another host. | The provider constructor returns an error, not nil, and accepts an empty key for local endpoints. `BASE_URL` points at `http://127.0.0.1:8080`. | 1 |
| 11 | The client hard-codes temperature 0.7 | A streamed reply differed from the non-streamed reply at 0.7 in 2 of 2 tries, and matched at 0 | Temperature is a request field. Every check that compares two replies uses temperature 0. | 1, 5 |
| 12 | The first build order left parts out | It had no step for a router, a registry file, the in-flight limit, transcripts or a real cloud provider. The diagram fed usage only from `run`. | Steps 3, 4, 8 and 9 add them. The diagram is redrawn so every call passes the usage recorder. | 3, 4, 6, 8, 9 |
| 13 | Step 0 passed only by removing tool support from streaming | After fixing the first two errors, `streaming.go:44` still fails (a tool type mismatch). The build passes only after removing the tool-loading lines and one import from `AskStreaming`. It also passes with the four packages kept. | Step 0 says it stubs tool support in `AskStreaming` until step 5, and checks the deletion with a search for importers. | 0 |

Two more facts from the code on `develop`:

- `go build ./...` fails with 3 errors in `internal/openai`: a missing `ctx` argument, `c.client` where the field is `c.llm`, and a tool-type mismatch in the streaming code.
- `pkg/pool`, `pkg/concurrency`, `pkg/log` and `internal/lmstudio` have no non-test importers. `external.ToolRegistry` is never initialised, so `Register` would write to a nil map.

## State ownership

Each piece of state has one owner.

| State | Single owner |
|---|---|
| Weights, KV and prompt cache, sampling defaults | `mlx_lm.server` |
| The loaded model and its server-side id | `mlx_lm.server` |
| Which logical model name maps to which endpoint and exact server id | The registry file, read by the router |
| Conversation messages | The caller (`run`) |
| Run transcripts | The transcript store, written by `run` |
| Parsing Inkling's control tokens, thinking and tool calls | The local provider's decoder |
| In-flight limit per endpoint | The limiter in the provider stack |
| Tokens, latency, cost | The usage recorder and its ledger |
| Keys | The environment, read once by the cloud provider. The local provider needs none. |

## Build order

Each step ends with a check that must pass before the next step starts. The Needs column lists earlier steps and anything outside the repo. No step needs a later step.

Rules for every check:

- A check that compares two replies uses temperature 0.
- A streaming request always sends `stream_options.include_usage=true`, and a check fails if it is missing.
- A check marked "real server" needs `mlx_lm.server` running: `python -m mlx_lm server --model <model dir> --port 8080`.

| Step | Work | Needs | Check |
|---|---|---|---|
| 0 | Delete the four unused packages (`pkg/pool`, `pkg/concurrency`, `pkg/log`, `internal/lmstudio`). Fix the build in `internal/openai`: add `ctx` to the `external.Calculator` call and use `c.llm`. Stub tool support in `AskStreaming` (remove the tool-loading lines and the unused import) until step 5 replaces it. | none | A search finds no importers of the four packages. `go build ./... && go vet ./...` exits 0. |
| 1 | Add a `provider` package. It holds the domain types (`Request`, and `Response` with text, thinking, tool calls and usage), the `Provider` interface and one OpenAI-compatible HTTP implementation built from the `internal/openai` client. Temperature becomes a request field. The constructor returns an error, not nil, and accepts an empty key for local endpoints. | Step 0. Real server. `BASE_URL=http://127.0.0.1:8080` in `.env`. | Tests against an `httptest` fake server. A constructor test with an empty key. One real call at temperature 0 returns a `Response` (its text still has control tokens until step 2). |
| 2 | Add the Inkling decoder inside the local provider. It strips control tokens, splits thinking from text, and parses `<|content_invoke_tool_json|>` blocks (keys `name` and `args`) into tool calls with generated IDs. | Step 1 | Unit tests on fixtures captured from the server: a tool-call turn, a final-answer turn and a plain answer. A real call returns text with no `<|` and a calculator tool call with an ID and arguments 1234, multiply, 5678. |
| 3 | Add the registry file and the pure `router`. An entry holds the name, the endpoint, the exact server model id (the path from `/v1/models`, or `default_model`), the capabilities and the memory. At startup, check each local entry against the endpoint's `/v1/models` and refuse to start on a mismatch. | Step 1. Real server for the startup check. | Router unit tests. The startup check fails for the name `inkling-2-mini` and passes for the exact id. A test shows the provider never sends a model string that is not in the registry. |
| 4 | Add the in-flight limiter and queue, one per endpoint, with a default limit of 1 until the engine's concurrency is measured. | Step 1 | A fake server records overlap. With limit 1, two concurrent calls never overlap. With limit 2, they do. |
| 5 | Replace `AskStreaming` with chat-completions streaming. Always send `stream_options.include_usage=true` and pass deltas through the decoder. | Steps 1 and 2. Real server. | At temperature 0, the streamed decoded reply equals the non-streamed decoded reply. The final chunk has non-null usage. A test fails when the provider omits the option. |
| 6 | Add the usage recorder as a `Provider` decorator, with the JSONL ledger. The router builds every provider as recorder, then limiter, then provider, so no call skips it. | Steps 1, 4 and 5 | A streamed call and a non-streamed call each add one ledger line with non-null prompt and completion tokens. A test shows every provider the router returns is wrapped. |
| 7 | Add the tools registry (initialised, schema generated from code, replacing `external.ToolRegistry` as it is now) and the `agent` loop with full message history and tool-call IDs. The loop sends the assistant `tool_calls` message and the `tool` message with `tool_call_id` back to the engine. | Steps 2, 3 and 6. Real server. | A spy on the calculator tool is called with multiply, 1234 and 5678, and the final text has 7,006,652. The ledger has at least 2 lines for the run. The same test fails when the loop is switched off, because the model alone also answers 7,006,652 without tools. |
| 8 | Add the transcript store. One JSONL file per run holds every message and tool call. The verdict field is added in step 10. | Step 1 | A write and read round trip. One file per run id. |
| 9 | Add the cloud provider, a second OpenAI-compatible implementation. It reads its key from the environment once, when built, and never logs it. | Steps 1, 3 and 6. A cloud API key for the real call. | A missing key gives an error. The key is absent from logs. One real call when a key is set, skipped otherwise. |
| 10 | Add `run`: the local answer, then the cloud check, then a verdict (agreed or disagreed). It writes the transcript. | Steps 7, 8 and 9 | A fake verifier that disagrees gives "disagreed", and the transcript records it. One real run once a cloud key exists. |
| 11 | Optionally add `cmd/gatewayd`. | Step 10 | A non-Go client gets a reply through one OpenAI-compatible URL. |

## Assumptions

- This is a single user on one Mac. If it is not, problems 3 and 4 change.
- No non-Go client needs an endpoint yet. If one does, step 11 moves earlier.
- `internal/lmstudio` is deleted in step 0. Keep it only if LM Studio stays as a second local backend.
- The engine's behaviour under concurrent requests is not measured, so the limit in step 4 starts at 1.
- Inkling's wire format is known only from the outputs seen in the reviews. The decoder fixtures in step 2 come from them, and a model update may change the format.
