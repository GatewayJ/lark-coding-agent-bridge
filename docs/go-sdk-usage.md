# Go SDK Usage

This package exposes the bridge as an embeddable Go SDK:

```go
import bridge "github.com/zarazhangrui/lark-coding-agent-bridge/pkg/bridge"
```

Use `pkg/bridge` only from outside the module. Packages under `internal/` are
implementation details for the CLI, adapters, config stores, and test fixtures.

## Quick Start

The SDK is distributed as a Go module, not through the npm package. The npm
artifact remains the JavaScript CLI/package distribution path; Go hosts should
depend on the module directly:

```sh
go get github.com/zarazhangrui/lark-coding-agent-bridge/pkg/bridge@latest
```

Install the CLI binary only when the host wants CLI-equivalent profile/service
flows or needs a `secrets get` executable for bridge exec secrets:

```sh
go install github.com/zarazhangrui/lark-coding-agent-bridge/cmd/lark-channel-bridge@latest
```

The module targets Go 1.23 or newer. Node.js is not required for ordinary SDK
embedding. It is only needed for JavaScript-side tooling, npm package usage, or
when a host explicitly opts into the legacy JavaScript telemetry module.

## Which Entry Point To Use

| Goal | Recommended API |
| --- | --- |
| Create a profile config from a host application | `BootstrapProfileConfig` |
| Run an already configured profile in the current process | `NewProfileBridge` |
| Start an already configured profile as a daemon | `StartProfileService` |
| Build a service controller for start/stop/status/restart | `NewProfileServiceController` |
| Embed a foreground bridge with an injected Lark transport | `New` with `Options{Client/CodexClient, LarkTransport, AppID, AgentKind}` |
| Own the Lark or Feishu channel startup yourself | `New` with `RuntimeAdapter` and `AppID` |
| Render JS-compatible run cards | `InitialState`, `Reduce`, `RenderCard`, `RenderText` |
| Wire custom telemetry | `LoadTelemetryAdapterFromEnv`, `TelemetryAdapter`, `SetDefaultTelemetry`, `ReportEvent` |

For CLI-equivalent behavior, prefer the profile bridge/service APIs. They load the
profile config, materialize env-backed app secrets into the profile keystore,
run the lark-cli source projection/preflight path, preserve Feishu/Lark tenant
selection, and use the same runtime locks as the foreground CLI.

`bridge.New` is the lower-level foreground constructor. It starts only the
capabilities you inject through `Options`: agent client, Lark transport/intake,
or a custom runtime adapter. It does not create or migrate profiles, install an
OS service, resolve profile app secrets, or run `PreflightLarkCLI` unless the
host wires those steps separately.

## Stable Surface And Advanced Surface

The stable SDK surface for ordinary hosts is intentionally small:
`BootstrapProfileConfig`, `NewProfileBridge`, `StartProfileService`,
`NewProfileServiceController`, `New`, the Lark/Feishu transport interfaces,
card rendering helpers, lark-cli projection/preflight helpers, and telemetry
interfaces.

`pkg/bridge` also exports lower-level command views, card dispatchers, runtime
registry/lock helpers, config and secret-store wrappers, and fake transports.
Those APIs exist to preserve JavaScript package behavior, support migrations,
and let advanced hosts replace individual subsystems. Prefer the stable entry
points above unless the host genuinely needs to own that subsystem boundary.

For API-level details, see [pkg/bridge operational facade](pkg/bridge.md).

## Compatibility Contract

The Go SDK keeps the JavaScript bridge behavior available through public Go
entry points where that behavior is useful to an embedding host:

| JavaScript capability | Go SDK surface |
| --- | --- |
| Profile v2 config, active profile, default workspaces, permissions, and app secrets | `BootstrapProfileConfig`, `LoadConfig`, `SaveConfig`, `NewProfileBridge`, `StartProfileService` |
| Codex and Claude execution, including Codex argv/env compatibility and bridge prompt injection | `NewCodexClient`, `NewClaudeClient`, `NewProfileBridge`, `New` |
| Feishu/Lark IM intake, cards, commands, comments, COT progress, media attachments, quote context, and incremental scope grants | `NewProfileBridge`, `New` with `LarkManaged`, `NewOAPILarkTransport`, `CommentSurface` |
| lark-cli source projection, exec secret forwarding, bind/preflight, identity policy, and local-user import checks | `WriteLarkCLISourceProjection`, `PreflightLarkCLI`, profile bridge/service preflight |
| Workspace persistence and `/cd`/`/ws` command behavior | `NewFileWorkspaceStore`, `CommandWorkspaceStore`, `NewCommandHandler`, managed command wiring |
| JavaScript-compatible run card state, card JSON, text rendering, and telemetry envelope | `InitialState`, `Reduce`, `RenderCard`, `RenderText`, `RequiredObservabilityEvents`, telemetry adapters |

Some behavior remains intentionally CLI-owned because it is interactive or
machine-mutating rather than an embeddable library concern: global `lark-cli`
installation, QR/PersonalAgent registration wizard, and human profile
management flows such as `profile create/use/remove/export`. Hosts can invoke
the CLI for those first-run UX flows, then use the SDK for steady-state service
or in-process operation.

## Profile Bootstrap

Use `BootstrapProfileConfig` when the host application wants to create the same
v2 profile shape that the CLI writes, without hand-authoring `config.json`.

```go
snapshot, err := bridge.BootstrapProfileConfig(bridge.BootstrapProfileOptions{
    RootDir:          "/var/lib/lark-channel",
    Profile:          "codex",
    AgentKind:        bridge.ConfigAgentCodex,
    AppID:            "cli_xxx",
    AppSecret:        bridge.SecretReference(bridge.SecretRef{Source: bridge.SecretSourceEnv, ID: "LARK_APP_SECRET"}),
    Tenant:           bridge.LarkCLITenantFeishu,
    DefaultWorkspace: "/srv/workspaces/codex",
})
if err != nil {
    return err
}
fmt.Println(snapshot.ProfileName)
```

The helper validates the full profile before writing `config.json` and
`active-profile`, fills attachment, permission, lark-cli identity, default
workspace, and Codex defaults, and refuses to overwrite an existing config unless
`Overwrite` is set. `AppSecret` accepts `PlainSecret(...)` or
`SecretReference(SecretRef{...})`. Advanced callers can still use `LoadConfig`,
`NormalizeConfig`, `SaveConfig`, and
`WriteActiveProfile` directly.

## Production CLI-Equivalent Integration

A production host that wants behavior matching `lark-channel-bridge start`
should provision the profile once, then start the profile through the service
facade. That path keeps the same profile layout, lark-cli binding, app-secret
materialization, runtime locks, and OS service semantics as the CLI.

```go
func provisionAndStart(ctx context.Context) error {
    root := "/var/lib/lark-channel"
    profile := "codex"

    if _, err := bridge.BootstrapProfileConfig(bridge.BootstrapProfileOptions{
        RootDir:          root,
        Profile:          profile,
        AgentKind:        bridge.ConfigAgentCodex,
        AppID:            "cli_xxx",
        AppSecret:        bridge.SecretReference(bridge.SecretRef{Source: bridge.SecretSourceEnv, ID: "LARK_APP_SECRET"}),
        Tenant:           bridge.LarkCLITenantFeishu,
        DefaultWorkspace: "/srv/workspaces/codex",
    }); err != nil {
        return err
    }

    result, err := bridge.StartProfileService(ctx, bridge.ProfileServiceOptions{
        Home:             root,
        Profile:          profile,
        Executable:       "/usr/local/bin/lark-channel-bridge",
        RequireConnected: true,
    })
    if err != nil {
        return err
    }
    fmt.Println("bridge service connected:", result.Process.BotName)
    return nil
}
```

On ordinary restarts, call `StartProfileService` against the existing profile
instead of bootstrapping again. Use `BootstrapProfileConfig` only during
provisioning or explicit reconfiguration.

The SDK entry points are non-interactive. They do not install `lark-cli`
globally and they do not run the CLI's QR/PersonalAgent registration wizard.
Hosts that need those first-run UX flows should invoke the CLI flow, or install
and validate dependencies before calling the SDK. `BootstrapProfileConfig`
expects explicit app credentials or a secret reference supplied by the host.

## In-Process Profile Bridge

Use `NewProfileBridge` when the host wants the foreground bridge behavior of
`lark-channel-bridge start` without registering an OS service. It loads the
profile, resolves or materializes the app secret, creates the OAPI transport,
wires Codex/Claude, lark-cli projection/preflight, callback nonces, persistent
workspace state, JSONL logs, and optional JavaScript telemetry.

```go
instance, info, err := bridge.NewProfileBridge(ctx, bridge.ProfileBridgeOptions{
    Home:                 "/var/lib/lark-channel",
    Profile:              "codex",
    LogDir:               "/var/log/lark-channel",
    SecretsGetterCommand: "/usr/local/bin/lark-channel-bridge",
})
if err != nil {
    return err
}
fmt.Println(info.Profile, info.AppID)

if err := instance.Start(ctx); err != nil {
    return err
}
defer instance.Shutdown(context.Background())
```

Important options:

- `SecretsGetterCommand` should point to a binary that supports
  `secrets get`. It is required when the profile app secret is a bridge exec
  secret, including when `AppSecret` overrides the configured secret, unless the
  host process itself is the `lark-channel-bridge` binary.
- `AppSecret` can override the configured secret and stores it in the profile
  keystore before startup.
- `SkipCheckLarkCLI` and `SkipAgentAvailability` are mainly for tests or hosts
  that already ran equivalent checks.
- `LoadTelemetryFromEnv` explicitly opts into loading the legacy JavaScript
  telemetry module named by `LARK_CHANNEL_TELEMETRY_MODULE`. It is disabled by
  default so constructing a profile bridge does not spawn Node.js.
- `LogDir` overrides the default JSONL log directory. If empty, logs are written
  under `<Home>/profiles/<Profile>/logs`.
- `LarkTransport`, `Logger`, `Telemetry`, `CommandOptions`, and
  `AccountValidator` let production hosts replace the default OAPI,
  observability, command, and credential-validation wiring.

## Profile Service

`StartProfileService` is the highest-level SDK path for host programs that want
the bridge managed by launchd, systemd user units, or Windows Task Scheduler.

```go
result, err := bridge.StartProfileService(ctx, bridge.ProfileServiceOptions{
    Home:             "/var/lib/lark-channel",
    Profile:          "codex",
    Executable:       "/usr/local/bin/lark-channel-bridge",
    RequireConnected: true,
})
if err != nil {
    return err
}
fmt.Println(result.Process.BotName)
```

Important options:

- `Home`, `Profile`, and `Config` select the profile root and config file.
- `Agent` can force the profile agent kind when the profile name is ambiguous.
- `Executable` is used both for the OS service and for the lark-cli exec secret
  wrapper. Set it when the host process is not the bridge executable.
- `RequireConnected` turns a service-start timeout into an error when no bridge
  process appears in the runtime registry before `WaitTimeout`.
- `SkipCheckLarkCLI` disables lark-cli preflight only when the host has already
  done equivalent source projection/bind work.
- `LarkCLICommand`, `LarkCLIBaseEnv`, and `LarkCLIRunner` let tests or embedding
  hosts customize how lark-cli commands are invoked.

Use `NewProfileServiceController` when the host needs separate
`Start`, `Stop`, `Restart`, `Status`, and `Unregister` calls.

## Embedded Foreground Bridge

Use `New` when the host wants to run a bridge in-process. Always set
`AgentKind` when the SDK cannot infer it from a `CodexClient` or `ClaudeClient`;
invalid agent kinds and tenants are rejected instead of silently defaulting.

```go
transport := bridge.NewFakeLarkTransport(bridge.LarkBotIdentity{
    OpenID: "ou_bot",
    Name:   "Bridge Bot",
})

instance, err := bridge.New(bridge.Options{
    Home:          homeDir,
    Profile:       "codex",
    AppID:         "cli_xxx",
    AgentKind:     bridge.RuntimeAgentCodex,
    CodexClient:   &bridge.CodexClientOptions{Binary: "codex"},
    LarkTransport: transport,
})
if err != nil {
    return err
}
if err := instance.Start(ctx); err != nil {
    return err
}
defer instance.Shutdown(context.Background())
```

When `LarkTransport` and a client are provided without a custom `LarkIntake`,
the SDK wires the managed intake for IM messages, slash commands, document
comments, COT messages, card updates, callbacks, and media attachments.
Production callers normally use `NewOAPILarkTransport`; tests can use
`NewFakeLarkTransport`.

If you call `Client.Run` directly, compute `RunInput.Access` with
`client.EvaluateAccess(...)`; handcrafted allow decisions are rejected so
untrusted callers cannot bypass the profile allowlist. `Client.HandleCommand`
can evaluate command access itself when `CommandRequest.Access` is omitted.
Long-lived hosts can create `NewCommandHandler(client)` and keep that handler
for an explicit command-state lifetime instead of relying on the
backwards-compatible package-level state behind `Client.HandleCommand`.
If a host does use `Client.HandleCommand` while rotating clients dynamically,
call `client.ReleaseCommandState()` before discarding the client.
Managed OAPI/Fake Lark transports automatically provide the `/new chat [name]`
group-creation path. Hosts that call `HandleCommand` without managed Lark
intake should pass `CommandOptions.ChatCreator` when they want `/new chat` to
create a Feishu/Lark group and invite the sender.

## Custom Runtime

Use `RuntimeAdapter` when the host owns channel startup or process supervision.
`RuntimeAdapterFunc` keeps small adapters terse:

```go
type hostRuntimeHandle struct{}

func (hostRuntimeHandle) Shutdown(context.Context) error { return nil }

func (hostRuntimeHandle) Status(context.Context) (bridge.RuntimeAdapterStatus, error) {
    return bridge.RuntimeAdapterStatus{Connected: true, BotName: "Bridge Bot"}, nil
}

runtimeAdapter := bridge.RuntimeAdapterFunc(
    func(ctx context.Context, req bridge.RuntimeStartRequest) (bridge.RuntimeHandle, error) {
        return hostRuntimeHandle{}, nil
    },
)

instance, err := bridge.New(bridge.Options{
    Home:           homeDir,
    Profile:        "codex",
    AppID:          "cli_xxx",
    AgentKind:      bridge.RuntimeAgentCodex,
    RuntimeAdapter: runtimeAdapter,
})
```

The runtime coordinator records profile/app locks and registry metadata around
the adapter. If shutdown fails, state remains retryable instead of being marked
stopped prematurely.

## Lark CLI Compatibility

The Go SDK preserves the JavaScript bridge's lark-cli integration:

- `BuildLarkChannelEnv` builds the `LARK_CHANNEL=1` environment.
- `WriteLarkCLISourceProjection` writes the bridge source config for lark-cli.
- `PreflightLarkCLI` runs the source projection, `config bind --source
  lark-channel`, identity policy, and local-user import checks.
- `StartProfileService` runs the preflight automatically unless
  `SkipCheckLarkCLI` is set.

The canonical public helper name is `PreflightLarkCLI`, with `CLI` in all caps.
Older mixed-case `LarkCli*` names are compatibility aliases and are not used in
new examples.

Use the high-level profile/service APIs when you need the full CLI-equivalent
path. Use the lower-level helpers only when the host owns the surrounding
profile and credential lifecycle.

For low-level foreground embedding, pass a persistent store explicitly when you
want `/cd` and `/ws` state to survive restarts:

```go
workspaces, err := bridge.NewFileWorkspaceStore(filepath.Join(profileDir, "workspaces.json"))
if err != nil {
    return err
}
transport := bridge.NewFakeLarkTransport(bridge.LarkBotIdentity{OpenID: "ou_bot"})
instance, err := bridge.New(bridge.Options{
    Home:      homeDir,
    Profile:   "codex",
    AppID:     "cli_xxx",
    AgentKind: bridge.RuntimeAgentCodex,
    CodexClient: &bridge.CodexClientOptions{
        Binary:            "codex",
        ProfileStateDir:   profileDir,
        DefaultWorkingDir: "/srv/workspaces/codex",
    },
    LarkTransport: transport,
    LarkManaged: bridge.LarkManagedOptions{
        InitialOwnerOpenID: "ou_creator",
        CommandOptions: bridge.CommandOptions{Workspaces: workspaces},
    },
})
```

`InitialOwnerOpenID` is optional. Use it when your host already knows the app
creator/owner from a QR registration or onboarding flow; the bridge still
refreshes the canonical app owner from Feishu after startup.

## Cards And Telemetry

The JavaScript package-root rendering helpers are mirrored:

```go
delta := "hello"
state := bridge.InitialState()
state = bridge.Reduce(state, bridge.Event{
    Type:  bridge.EventText,
    Delta: &delta,
})
card := bridge.RenderCard(state, bridge.CardRenderOptions{})
text := bridge.RenderText(state)
```

`RunState` JSON uses the JavaScript shape: `blocks` is an array and terminal
states encode `footer` as `null`.

Telemetry is optional and best-effort. Adapter panics and flush/close failures
do not break bridge startup or shutdown. Emitted events include the JavaScript
fields `level`, `phase`, `event`, `fields`, `ctx`, and `ts`, plus Go convenience
fields `name` and `at`.

Use `RequiredObservabilityEvents()` to get the JS-compatible required event
names that production telemetry adapters should preserve.

For JS-compatible telemetry modules, set `LARK_CHANNEL_TELEMETRY_MODULE` and
load the adapter through the Go helper:

```go
telemetry, err := bridge.LoadTelemetryAdapterFromEnv(ctx, bridge.AdapterMeta{
    Version:  "host-version",
    AppID:    "cli_xxx",
    Tenant:   "feishu",
    Hostname: hostname,
}, os.Stderr)
if err != nil {
    return err
}
if telemetry != nil {
    defer telemetry.Close(context.Background())
}
```

## Verification

The repository keeps SDK usage covered by:

```bash
go test -mod=readonly ./...
go test -mod=readonly -race ./internal/app/runtimecoord ./pkg/bridge ./internal/adapters/lark ./internal/app/service
go vet -mod=readonly ./...
pnpm test:go:cross
pnpm test:go:tracked
pnpm test:go:head
pnpm test
pnpm typecheck
pnpm build
```

`tests/external_go_smoke` creates a separate Go module and imports `pkg/bridge`
as an external consumer through a local module replacement. Use that test when
adding public SDK symbols or usage examples.

Before committing or publishing, run `pnpm test:go:tracked` to verify the Go SDK
files are part of Git and no SDK file is left untracked. After committing and
before tagging, run `pnpm test:go:head` to verify the commit itself contains the
SDK file set. Then run a real published import check after the commit/tag is
reachable:

```bash
GO_SDK_IMPORT_VERSION=<commit-or-tag> pnpm test:go:published
```

The published check intentionally avoids a local `replace`, so it proves a
fresh external Go module can `go get` and import `pkg/bridge`.
For a fork or candidate branch before the upstream commit is reachable, keep the
upstream import path in the smoke test and resolve it through the candidate
module:

```bash
GO_SDK_REPLACE_MODULE=<fork-module> GO_SDK_IMPORT_VERSION=<commit-or-tag> pnpm test:go:published
```
