# Codex Feishu SDK Example

This example starts a small Go host that embeds `pkg/bridge`, connects a
Feishu/Lark app to the local `codex` CLI, and keeps running until `Ctrl+C`.

Prerequisites:

- local `codex` CLI is installed and logged in;
- a Feishu/Lark PersonalAgent app already exists;
- `lark-cli` is installed if you want the normal bridge preflight path.

If you do not have a PersonalAgent app yet, run the Go CLI once from an
interactive terminal and use the printed Feishu/Lark creation link:

```sh
go run ./cmd/lark-channel-bridge run --agent codex
```

Run:

```sh
export LARK_APP_ID=cli_xxx
export LARK_APP_SECRET=xxx
export LARK_CHANNEL_EXAMPLE_OWNER_OPEN_ID=ou_xxx
export LARK_CHANNEL_EXAMPLE_ALLOWED_USERS=ou_xxx

go run ./examples/codex-feishu
```

`LARK_CHANNEL_EXAMPLE_OWNER_OPEN_ID` is the app creator/owner `open_id`; when
present it is used as an initial owner fallback while the runtime still refreshes
the app owner from Feishu. `LARK_CHANNEL_EXAMPLE_ALLOWED_USERS` is a
comma-separated list of user `open_id`s allowed to DM the bot. Empty access lists
are intentionally deny-by default.

Optional variables:

- `LARK_TENANT=lark` for Lark global; default is `feishu`.
- `LARK_CHANNEL_EXAMPLE_HOME=/tmp/lark-channel-demo` to choose the profile root.
- `LARK_CHANNEL_EXAMPLE_LOG_DIR=/tmp/lark-channel-demo-logs` to choose the JSONL log directory.
- `LARK_CHANNEL_EXAMPLE_WORKSPACE=/path/to/repo` to choose the Codex workspace.
- `LARK_CHANNEL_EXAMPLE_SKIP_LARK_CLI=1` to skip lark-cli preflight for a quick smoke test.
