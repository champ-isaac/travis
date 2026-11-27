# Inkling fixtures

Each `*.json` file is one real reply from `mlx_lm.server` serving Inkling 2 Mini (4-bit), captured on 2026-11-27 through `POST /v1/chat/completions` at temperature 0 without streaming. `content` is the exact text the server returned, control tokens included. `request` holds the prompt and tools that produced it. The tool is `schemas/basic_calculator.json`.

| File | What it is |
|---|---|
| `plain.json` | thinking, then a text answer, no tools offered |
| `single_tool_call.json` | thinking, then one calculator call |
| `multiple_tool_calls.json` | thinking, then two calculator calls in one reply |
| `final_answer.json` | thinking, then a text answer, after a tool result was sent back |

Checked when captured: every reply finished with `stop`, a repeat run gave the same text, and the streamed deltas joined to the same text. Replies are not deterministic across model or server updates, so a format change shows up here first. Re-capture by sending the `request` to a running server and replacing `content`.
