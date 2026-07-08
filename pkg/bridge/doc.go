// Package bridge exposes the Go SDK surface for embedding the Lark coding
// agent bridge in another Go program.
//
// Most embedders should use the stable profile/service facade:
// BootstrapProfileConfig, NewProfileBridge, StartProfileService, or
// NewProfileServiceController. Use New only when the host owns lower-level
// runtime, Lark/Feishu transport, or agent-client wiring.
//
// The package also exposes compatibility and advanced helpers that preserve
// the JavaScript package exports while keeping internal package types behind
// public Go wrappers. Those helpers are useful for custom hosts, migrations,
// tests, and operational tooling, but they are not the first-choice embedding
// entry points for ordinary SDK consumers.
//
// Codex and Claude execution, prompt injection, run policy, session/thread
// resume, slash commands, managed Lark IM intake, config and secret stores,
// Lark intake normalization, card rendering, account/config form cards, Codex
// history lookup, Lark CLI compatibility helpers, callback token auth, runtime
// locks/registry, media attachment cache, document-comment handling, managed
// cards, optional quoted-message prompt context, and an optional production
// Feishu/Lark OpenAPI transport are available here.
//
// Embedded callers can use the SDK surface directly, including the same
// run/start service path used by the JavaScript CLI.
package bridge
