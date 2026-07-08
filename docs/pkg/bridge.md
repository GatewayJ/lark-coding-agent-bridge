# pkg/bridge operational facade

`pkg/bridge` exposes the Go SDK surface for embedding the bridge:

For a task-oriented guide with service, embedded-runtime, lark-cli, card, and
telemetry examples, see [Go SDK Usage](../go-sdk-usage.md).

The Go SDK is consumed as a Go module. The npm package remains the JavaScript
CLI/package distribution path and should not be treated as the Go source
artifact for external Go hosts.

```go
type Bridge struct{}

type Options struct {
    Home    string
    Profile string

    Logger    Logger
    Telemetry TelemetryAdapter

    Client       *Client
    CodexClient  *CodexClientOptions
    ClaudeClient *ClaudeClientOptions

    LarkTransport              LarkTransport
    LarkAdapter                *LarkAdapter
    LarkIntake                 LarkIntakeSink
    LarkCardActions            LarkCardActionDispatcher
    LarkComments               CommentSurface
    LarkCommentOptions         CommentOptions
    LarkManaged                LarkManagedOptions
    LarkProfileProjection      LarkProfileProjectionHook
    ForwardCardPromptsToIntake bool
    RuntimeAdapter             RuntimeAdapter
    AppID                      string
    Tenant                     RuntimeTenant
    AgentKind                  RuntimeAgentKind
    Version                    string
    ConfigPath                 string
}

func New(opts Options) (*Bridge, error)
func (b *Bridge) Start(ctx context.Context) error
func (b *Bridge) Shutdown(ctx context.Context) error
func (b *Bridge) Status(ctx context.Context) (Status, error)
```

Use `StartProfileService`, `NewProfileServiceController`, and
`BootstrapProfileConfig` for CLI-equivalent production integration. Those APIs
assemble the profile config, app-secret materialization, lark-cli projection,
`PreflightLarkCLI`, runtime locks, and OS service wiring around the same bridge
runtime. `New` is intentionally lower-level: it starts only the injected client,
transport/intake, or custom runtime adapter, so the host owns any surrounding
profile, credentials, lark-cli, and service lifecycle.

Supported embedding modes:

- agent-only: pass `Client`, `CodexClient`, or `ClaudeClient`; `Start` loads state and `Shutdown` stops and flushes the client;
- injected Lark channel: pass `LarkTransport` plus `AppID`; `Start` wraps it in `LarkAdapter` and `RuntimeCoordinator` for profile/app locks and registry status. If a `Client` is also provided and no custom `LarkIntake` is supplied, the SDK auto-wires a managed intake for IM messages, slash commands, and document comments when a `LarkComments` surface or OAPI transport is available;
- custom runtime: pass `RuntimeAdapter` plus `AppID` when the host owns channel startup. `RuntimeAdapterFunc` is available for small host-side adapters.

Public lark-cli helpers use the canonical all-caps `CLI` spelling:

```go
func BuildLarkChannelEnv(context LarkCLIEnvContext) map[string]string
func BuildLarkCLISourceProjection(cfg LarkCLIAppConfig, paths LarkCLIProjectionPaths) LarkCLISourceProjection
func WriteLarkCLISourceProjection(cfg LarkCLIAppConfig, paths LarkCLIProjectionPaths) (string, error)
func PreflightLarkCLI(ctx context.Context, options LarkCLIPreflightOptions) (LarkCLIPreflightResult, error)
```

Command handling can be used directly through `Client.HandleCommand` for
backwards-compatible state, or through `NewCommandHandler(client)` when a host
wants an explicit per-handler lifetime for `/cd`, `/ws`, resume, and timeout
state. Hosts that use `Client.HandleCommand` while rotating clients can call
`client.ReleaseCommandState()` before discarding a client. Managed OAPI/Fake
Lark transports provide `/new chat [name]` automatically; direct command hosts
can pass `CommandOptions.ChatCreator` to enable group creation with sender
invitation.

Minimal external embedding example:

```go
instance, err := bridge.New(bridge.Options{
    Home:          homeDir,
    Profile:       "codex",
    AppID:         "cli_xxx",
    AgentKind:     bridge.RuntimeAgentCodex,
    LarkTransport: bridge.NewFakeLarkTransport(bridge.LarkBotIdentity{OpenID: "ou_bot"}),
    LarkIntake: bridge.LarkIntakeSinkFunc(func(context.Context, bridge.LarkNormalizedEvent) error {
        return nil
    }),
})
if err != nil {
    return err
}
if err := instance.Start(ctx); err != nil {
    return err
}
defer instance.Shutdown(context.Background())
```

The Go module now includes a production Feishu/Lark OpenAPI transport and
document-comment surface. The managed IM path currently covers access checks,
group mention policy, slash-command-first handling, debounce batching, media
attachment resolution with JavaScript-compatible optional attachment
degradation, optional quoted-message and current interactive-card prompt
context, JavaScript-compatible sender metadata in prompt context, multi-sender
batch annotations, attachment-reference cleanup, Codex / Claude runs, Lark CLI
bridge env projection,
card-action access gating, `markdown`
mode that streams by updating one message when the transport supports message
patching and falls back to a final reply otherwise, `text` mode final replies,
and `card` mode that sends an initial active card, signs the stop action when
callback auth is configured, then updates the same card during the run and at
the terminal result when the transport supports updates. Run presentation
respects configured reply mode, tool-call visibility, and idle watchdog
settings, including `/config submit` updates and per-scope `/timeout`
overrides. When `LarkManaged.CotMessages` or `/config` enables COT process
messages and the transport implements `LarkCOTClient`, the managed intake
creates a Feishu/Lark COT message for process text and tool progress, then
sends a separate final-answer-only reply. The production OAPI transport and
fake transport both implement the COT client interface, including create,
update, completion, and update-failure degradation behavior. Non-card reply
modes add a best-effort `Typing` reaction to the triggering message and clean
it up after the run when the transport supports message reactions. Managed
command responses for `/config` and `/account` now render the Go CardKit forms
and update the originating card action when possible. Form submit actions for
`/config submit` and `/account submit` detach from the callback and wait for the
CardKit settle window before updating the original card, matching the JavaScript
client workaround. `/config submit` can apply and roll back the lark-cli identity
policy through the SDK hook, and `/account submit` can validate self-built app
credentials, persist the secret through the exec secret provider, and request a
reconnect through the SDK reconnector hook. Account reconnects are scheduled
after the success card update attempt and are deduplicated while a restart is
pending; failed account submits turn the original form card into a static
failure record and send a fresh retry form without carrying the submitted
secret forward. The OAPI transport now supplies the default quote resolver,
scope checker, and incremental scope-grant requester. After `/config submit`
enables non-mention group messages, the managed intake checks
`im:message.group_msg`, sends the incremental-authorization card if the scope
is missing, and updates that card when authorization completes. Hosts with a
custom transport can provide `LarkManaged.ScopeChecker` and
`LarkManaged.ScopeGrant` to get the same behavior. The same OAPI transport also
feeds managed runtime information: the bot owner is refreshed into access
controls, and known group chats are refreshed into `/config` form options.
Custom transports can provide `LarkManaged.RuntimeInfo` for equivalent owner
and known-chat behavior. The Go CLI can now bootstrap a profile v2 config for
the first foreground run either from an interactive Lark registration link or
from supplied `--app-id` / `--app-secret` credentials; it writes the active
profile, profile-local encrypted app secret, root `bridge` exec secret
provider, default workspace, and Codex binary metadata for explicit Codex
profiles. When neither `--profile` nor `--agent` is supplied on first run, it
detects installed Claude/Codex agents and follows the JavaScript selection
rules. Supplied app credentials are validated before being stored. The Go CLI
also exposes foreground-process `ps` and `kill`
commands backed by the runtime registry; `kill` first asks the process to stop
and only force-kills it after the grace window. Managed `/ps` and `/exit`
commands are wired to the same registry for foreground Go CLI runs, using the
runtime short process ID instead of the OS PID in user-facing command replies.
The Go CLI now covers the basic profile operations `profile create`, `profile
list`, `profile use`, `profile export` with safe redaction by default plus
explicit `--include-secrets --yes` plaintext export, and `profile remove` with
archive/purge handling. Human-facing encrypted secret management is available
through `secrets set/list/remove` while preserving the exec-provider
`secrets get` protocol for lark-cli. The Go CLI also provides a `migrate`
command for legacy v1 config/state migration into the profile v2 layout,
including config backup, profile state moves, active-profile sidecar creation,
and active legacy process blocking.
For initialized profiles, the Go CLI now rejects a mismatched `--app-id`
override instead of running against one app while the profile still records
another. The Go SDK also exposes lark-cli preflight and OS service controller
facades. The service facade uses public SDK types for adapter results, service
definitions, process listing, and runtime-lock hooks, so embedding programs do
not need to import internal packages. `StartProfileService` is the high-level
SDK path for starting an already configured profile as an OS-managed daemon; it
loads the profile config, materializes env-backed app secrets into the profile
keystore for daemon use, and can run the same lark-cli preflight/bind path as
the CLI. The CLI `run` command runs the bridge in the foreground, while
`start`, `stop`, `restart`, `status`, and `unregister` operate the current
profile through launchd, systemd user units, or Windows Task Scheduler using
the same public service facade and `run --profile <name>` daemon entrypoint as
the JavaScript CLI. Service startup now mirrors the JavaScript order:
runtime-lock handling and lark-cli preflight happen before the old daemon is
stopped, env-backed app secrets are materialized into the profile keystore for
daemon use, and explicit service-profile cleanup still works after the profile
has been removed.
First-run startup also follows the JavaScript bootstrap behavior: it detects
installed Claude/Codex CLIs before selecting an agent profile, supports
interactive Lark app registration, and validates manually supplied app
credentials before storing them.

The package also mirrors the JavaScript package exports for card rendering and
telemetry helpers. Callers can use the JS-compatible names `InitialState`,
`Reduce`, `FinalizeIfRunning`, `MarkInterrupted`, `MarkIdleTimeout`,
`RenderCard`, and `RenderText`, or the richer Go `RunCardState` facades.
Telemetry adapters receive the JavaScript-shaped `TelemetryEvent` fields
(`level`, `phase`, `event`, `fields`, `ctx`, `ts`) as well as Go convenience
fields (`name`, `at`). Callers can install package-level telemetry with
`SetDefaultTelemetry`, `ReportEvent`, `ReportMetric`, and `ReportError`.
`SetDefaultTelemetry` is process-global; embedded SDK users should call the
returned restore function when a scoped adapter is no longer active.
