# Go SDK Migration Plan

This document is the corrected plan for porting `lark-channel-bridge` from
TypeScript to Go while preserving the current JavaScript behavior.

The Go implementation must be a library-first SDK with a thin CLI wrapper, but
it must not become a new product with similar behavior. Compatibility with the
current JavaScript version is the primary release gate.

## Non-Negotiable Contracts

### Behavior Parity

The Go version must preserve these visible behaviors:

- Feishu/Lark PersonalAgent binding and startup wizard.
- IM, group, topic group, card action, and cloud-document comment entry points.
- Streaming card, markdown, plain text, and COT message rendering modes.
- All slash commands documented in `README.zh.md`.
- Claude Code and Codex CLI subprocess integration.
- Per-scope session continuation for chats, topic groups, and comments.
- Debounce, batching, per-scope active-run exclusion, and global process pool.
- Attachment download, validation, cache, and prompt conversion.
- Access control, owner/admin bypass, and quiet denial behavior.
- Profile, workspace, secret, migration, runtime registry, daemon, and logging behavior.

### Compatibility Is Core, Not Adapter Detail

The following formats and protocols are part of the Go core compatibility
contract:

- `~/.lark-channel/config.json` v2 root/profile schema.
- Legacy v1 config migration and rollback behavior.
- `active-profile`.
- `sessions.json` and `sessions.json.catalog.json`.
- `workspaces.json`.
- `secrets.enc`, `.keystore.salt`, and `secrets-getter` / `secrets-getter.cmd`.
- `lark-cli-source/config.json` projection and profile-local `lark-cli` directory.
- `registry/processes.json` and profile/app lock metadata.
- callback token format `bridge_cb.v1`.
- Codex task CLI argv and Codex history app-server protocol.

### Public API Must Stay Small

The JavaScript package currently exposes rendering helpers and telemetry types,
not the full bot internals. Go v1 should expose a small operational SDK and keep
volatile orchestration internals under `internal/`.

Do not expose these as stable v1 APIs:

- raw agent adapter lifecycle (`PrepareRun`, `Run`, `SetBotIdentity`);
- raw card renderer/dispatcher/callback internals;
- direct `SubmitRun` that can bypass policy, session catalog, queueing, and access checks;
- migration internals, legacy config structs, keystore envelopes, lock files, or process registry rows.

## Corrected Architecture Shape

```text
cmd/lark-channel-bridge
  -> pkg/bridge facade
  -> internal/app use cases
  -> internal/domain policies and state machines
  -> internal/ports owned by app/domain
  -> internal/adapters for Lark, CLI agents, files, daemon, secrets
```

The shape is hexagonal, but the runtime coordinator is a first-class application
service. Lark, filesystem, daemon APIs, process execution, and optional
telemetry remain replaceable details.

There must be one owner for each orchestration concern:

- `RuntimeCoordinator`: process locks, registry, lifecycle, start, shutdown, and reconnect.
- `IntakeService`: translate Lark messages, card actions, and comments into app commands.
- `CommandService`: slash command parsing and command authorization.
- `RunFlow`: prompt construction, workspace resolution, access policy, session lookup.
- `RunExecutor`: process pool, active-run registry, agent lifecycle, event fanout.
- `CompatibilityService`: disk format codecs, config normalization, migration, and oracle fixtures.

No public SDK method may bypass `RunFlow` and call `RunExecutor` directly.
No channel-facing component may construct `RunExecutor` directly; runtime wiring belongs
to `RuntimeCoordinator`.

## Current Implementation State

The Go port now exposes `pkg/bridge` as the library-first SDK surface while the
CLI remains a thin wrapper over the same application services. The facade covers:

- `New`, `Start`, `Shutdown`, `Status`, and `Reconnect` for embedded runtime
  operation;
- Codex and Claude client construction through the same RunFlow, policy,
  session catalog, queueing, process-pool, and prompt paths used by managed
  Lark intake;
- managed Feishu/Lark IM, card action, comment, COT, quote, media, command, and
  owner/scope-refresh behavior;
- Lark CLI projection, identity policy, preflight, legacy source-overlay
  recovery, and profile-local environment helpers;
- service/daemon control through public SDK types, including
  `StartProfileService` for starting an already configured profile without
  copying CLI-private logic;
- JavaScript package export equivalents for run-card reduction/rendering and
  telemetry helpers.

The implementation keeps volatile orchestration under `internal/` and uses
explicit adapters at the public boundary. Compatibility helpers are exposed only
where callers need them to embed the bridge or to preserve JavaScript disk/API
contracts.

## Directory Structure

```text
cmd/lark-channel-bridge/
  main.go

pkg/bridge/
  bridge.go
  options.go
  status.go
  service.go
  profile_service.go
  telemetry.go

internal/app/
  commands/
  configstore/
  intake/
  larkcli/
  larkclipreflight/
  media/
  runexecutor/
  runflow/
  runtimecoord/
  secretstore/
  service/
  session/
  workspace/

internal/domain/
  access/
  permissions/
  profile/
  runpolicy/

internal/compat/
  apppaths/
  codex/

internal/presentation/
  prompt/

internal/ports/
  agent/

internal/adapters/
  agent/claudecli/
  agent/codexcli/
  codexhistory/
  lark/

testdata/compat/
  codex-jsonl/
  config/
  prompt/
  secrets/

tests/compat/
tests/external_go_smoke/
```

## Public SDK Interface

The stable v1 package is `pkg/bridge`. Representative entry points are:

```go
package bridge

type Bridge struct {
    // unexported fields
}

type Options struct {
    Home    string
    Profile string

    Logger    Logger
    Telemetry TelemetryAdapter

    Client         *Client
    CodexClient    *CodexClientOptions
    ClaudeClient   *ClaudeClientOptions
    LarkTransport  LarkTransport
    RuntimeAdapter RuntimeAdapter
    AppID          string
    AgentKind      RuntimeAgentKind
}

func New(opts Options) (*Bridge, error)
func (b *Bridge) Start(ctx context.Context) error
func (b *Bridge) Shutdown(ctx context.Context) error
func (b *Bridge) Status(ctx context.Context) (Status, error)

func StartProfileService(ctx context.Context, opts ProfileServiceOptions) (ServiceStartResult, error)
func NewServiceController(opts ServiceControllerOptions) ServiceController

func NewRunCardState(input RunCardStateInput) RunCardState
func ReduceRunCardState(state RunCardState, event Event) RunCardState
func RenderRunCard(state RunCardState, options CardRenderOptions) CardView
func RenderRunText(state RunCardState) TextView

func SetDefaultTelemetry(adapter TelemetryAdapter) func()
func ReportMetric(ctx context.Context, name string, value float64, tags map[string]string)
func ReportError(ctx context.Context, err error, fields map[string]any)
```

Extension points are intentionally narrow:

```go
type TelemetryAdapter interface {
    Emit(ctx context.Context, event TelemetryEvent)
    RecordError(ctx context.Context, err error, fields map[string]any)
    RecordMetric(ctx context.Context, name string, value float64, tags map[string]string)
    Flush(ctx context.Context) error
    Close(ctx context.Context) error
}

type Logger interface {
    Info(msg string, fields map[string]any)
    Warn(msg string, fields map[string]any)
    Error(msg string, fields map[string]any)
}
```

Custom runtime/channel behavior is supplied through public facade interfaces such
as `RuntimeAdapter`, `LarkTransport`, and service adapters; consumers do not need
to import `internal/` packages. Interactive first-run app registration remains a
CLI workflow, while SDK callers can operate already configured profiles through
`StartProfileService` or construct embedded runtimes directly with `New`.

## Compatibility Contracts

### Config And Paths

Go must read and write the same path layout:

- root dir from `LARK_CHANNEL_HOME` or `~/.lark-channel`;
- profile dir under `profiles/<profile>`;
- default workspace under `<root>-workspaces/<profile>/default`;
- media, logs, lark-cli, lark-cli-source, registry, and lock paths matching JS.

`NormalizeProfileConfig` parity must cover:

- `schemaVersion: 2`;
- `agentKind: claude|codex`;
- canonical `permissions` and legacy `sandbox` migration;
- preserved `messageReply` disk value, including `text`;
- effective reply-mode resolver that maps legacy `text` to `markdown` only when
  `messageReplyMigrated` is not true;
- COT legacy values `on` and `simple`;
- attachment defaults;
- lark-cli identity defaults;
- Codex `inheritCodexHome`, `ignoreUserConfig`, and `ignoreRules` upgrade behavior.

### Migration

Migration must be transactional:

1. detect active bridge processes and refuse unsafe migration;
2. acquire profile/app locks;
3. write a backup of the old config and state files;
4. migrate config, sessions, workspaces, secrets, media, and logs;
5. write `active-profile`;
6. rollback on failure when possible;
7. emit a migration report with changed paths.

### Secrets And Keystore

Go must be compatible with the current secret resolver:

- plain string;
- `${ENV_VAR}`;
- `SecretRef{source:"env"}`;
- `SecretRef{source:"file"}`;
- `SecretRef{source:"exec"}`;
- exec provider timeout and stdout-size limit;
- self-call short-circuit for the bridge secrets getter;
- local AES-256-GCM envelope format;
- PBKDF2-SHA256 100k key derivation with hostname, username, and salt;
- `0600` writes where supported.

This keystore remains defense-in-depth, not a same-user security boundary. A
future OS keychain provider may be optional, but cannot replace the existing
wire format during migration.

### Lark CLI

Go must preserve the profile-local lark-cli behavior:

- write `lark-cli-source/config.json`;
- create a bridge secrets getter wrapper;
- set `LARK_CHANNEL`, `LARK_CHANNEL_HOME`, `LARK_CHANNEL_PROFILE`,
  `LARK_CHANNEL_CONFIG`, and `LARKSUITE_CLI_CONFIG_DIR` for agent subprocesses;
- apply identity policy with `lark-cli config strict-mode` and `default-as`;
- preserve legacy source-overlay recovery.

The projection file shape belongs in `internal/app/larkcli`; invoking
`lark-cli`, preflight binding, and identity-policy recovery belongs in
`internal/app/larkclipreflight`.

### Session And Workspace

Go must preserve:

- `sessions.json` format, including per-scope idle-timeout overrides;
- session catalog key: `scopeId + "\x1f" + agentId + "\x1f" + cwdRealpath + "\x1f" + policyFingerprint`;
- Claude entries requiring `sessionId`;
- Codex entries requiring `threadId`;
- archive and GC rules;
- workspace store with per-scope `chats` and named workspaces;
- workspace validation rejecting root, home root, system roots, temp roots, and missing dirs.

### Codex

Codex must be split into two ports:

- `CodexExecutor`: task execution via `codex exec --json ... -`;
- `CodexHistoryProvider`: history listing via `codex app-server --listen stdio://`.

Do not use app-server or ACP as the task execution path unless the JavaScript
version changes first and parity fixtures are updated.

### Callback Tokens

Go must preserve `bridge_cb.v1` tokens:

- HMAC signature;
- run, scope, chat, operator, action, policy fingerprint binding;
- expiration check;
- nonce consume/replay protection.

The Go nonce store should improve the JS implementation with mutex-protected
consume, atomic writes, expiration-aware storage, and TTL GC while keeping token
verification compatible.

## Runtime Contracts

### Single Instance

`Bridge.Start` must acquire compatible profile and app locks by default and
register in `processes.json`. A caller can opt out only with an explicitly named
unsafe option, for tests and controlled embeddings.

### Process Lifecycle

Agent subprocess management must include:

- process group / job object support;
- stdout and stderr streaming;
- stderr byte cap;
- terminal event handling;
- post-terminal graceful wait;
- SIGTERM then SIGKILL after grace;
- cleanup of child process trees;
- Windows-specific termination behavior.

### Queueing And Reconnect

Go must preserve:

- per-scope debounce;
- pending queue block/unblock while a run is active;
- commands bypassing the normal message queue;
- active run reservation before acquiring process-pool slots;
- reconnect pause semantics;
- stop-all and store-flush on disconnect;
- clear user-visible rejection reasons for reconnect, pool-full, and active-run conflicts.

### Event Fanout And Backpressure

Do not replicate an unbounded event buffer. Define:

- one primary event consumer for state persistence and rendering;
- bounded replay for observers;
- slow-consumer handling;
- Lark update throttle;
- fallback from streaming update to final send when update fails.

### Lark Channel

The Lark adapter contract must include:

- Feishu vs Lark domain selection;
- proxy env support;
- WebSocket ping timeout;
- handshake timeout;
- REST timeout;
- SDK-noise filtering equivalent;
- keepalive/probe behavior;
- message, card action, and comment callbacks;
- raw event access when normalized events omit form values.

### Media Cache

Media download must:

- stream into temp files;
- hash content before final filename;
- avoid original names in paths;
- delete rejected files;
- preserve accepted files during cache trimming;
- enforce max count, max bytes, image max bytes, file max bytes, TTL, and cache max bytes;
- handle context cancellation and startup/periodic GC.

## Parity Test Oracle

The Go port cannot rely on hand-copied fixtures alone. JavaScript fixtures remain
the oracle for cross-language compatibility and future regression checks.

For each contract, run the same input through TS and Go and compare normalized
artifacts:

- config normalization;
- v1 to v2 migration;
- secret resolution;
- lark-cli projection;
- workspace validation;
- session catalog read/write;
- run policy fingerprint;
- callback token verify/sign;
- Codex argv;
- Claude/Codex stream translation;
- run-state reduction;
- card/text rendering;
- fake Lark IM flow;
- fake card callback flow;
- fake comment flow;
- daemon file generation.

## Implemented Slices

- Compatibility codecs: app paths, permissions, JCS policy fingerprints,
  session catalog, config normalization, lark-cli projection, Codex argv, Codex
  JSONL, keystore, and secret resolver.
- Runtime: run flow, run executor, process pool, active runs, pending queue,
  runtime locks, process registry, reconnect, and event fanout.
- Agents: Claude CLI, Codex CLI execution, and Codex history provider.
- Lark: fake transport, production OAPI transport, managed IM intake, card
  action dispatch, comments, quote context, media cache, COT presentation, and
  managed card updates.
- CLI and daemon: `run`, `start`, `stop`, `restart`, `status`, `unregister`,
  `profile`, `secrets`, `migrate`, `ps`, and `kill`, with launchd, systemd user
  service, and Windows Task Scheduler definitions.
- SDK facade: public service, runtime, Lark, command, card, config, secret,
  media, prompt, telemetry, persistent workspace, JSONL logger, config
  bootstrap, and profile-service APIs.

## Verification Matrix

The current local gate set is:

- `go test ./...`;
- `go test -race ./internal/app/runtimecoord ./pkg/bridge ./internal/adapters/lark ./internal/app/service`;
- `pnpm test:go:cross`;
- `pnpm test`;
- `pnpm typecheck`;
- `pnpm build`;
- `tests/external_go_smoke`, which imports `pkg/bridge` from a separate Go
  module and exercises OAPI construction, config bootstrap, persistent
  workspaces, telemetry helpers, service facade, and `StartProfileService`.

## Release Audit

Before publishing the Go module, repeat the gate set above in CI and check:

- JS-documented user flows have a Go test or compatibility fixture;
- Go can read existing JS user state without data loss;
- unsafe migration is refused when an old bridge process is active;
- Codex task execution still uses CLI `exec --json`;
- lark-cli identity policy works per profile;
- secret resolution and diagnostics do not log plaintext;
- callback replay is rejected;
- one app/profile cannot be started twice by default;
- daemon launches preserve PATH, state dir, profile, and logs;
- the public SDK exposes stable facades instead of requiring consumers to import
  `internal/` packages.
