# codex-app-server-go

> A typed Go client for the Codex app-server **v2** JSON-RPC protocol — drive AI coding agents from Go.

[![Go Reference](https://pkg.go.dev/badge/github.com/zealbase/codex-app-server-go.svg)](https://pkg.go.dev/github.com/zealbase/codex-app-server-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/zealbase/codex-app-server-go)](https://goreportcard.com/report/github.com/zealbase/codex-app-server-go)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go version](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go)](go.mod)
[![Protocol](https://img.shields.io/badge/protocol-stable%20surface-orange)](internal/protocol/schema/version.go)
[![Conformance](https://img.shields.io/badge/conformance-strict-blue)](Makefile)

## What's Implemented

<!-- coverage:start -->

Aligned to codex commit `14c8b7771ab2b617a131f5d8e55e98d18e56ed09`; scope = **stable** only.

Every number below is generated from `gen/method-surface.json` and `gen/implemented-methods.json`, and enforced by `make conformance-strict`.

### Client → server requests

| Subsystem | Implemented | Declared | Methods |
|---|:---:|:---:|---|
| Core / Initialize | 1 | 1 | `initialize` |
| Thread | 27 | 27 | `approveGuardianDeniedAction` · `archive` · `attachment/add` · `attachment/list` · `attachment/remove` · `attachmentOwner/list` · `compact/start` · `delete` · `fork` · `goal/clear` · `goal/get` · `goal/set` · `inject_items` · `items/list` · `list` · `loaded/list` · `metadata/update` · `name/set` · `read` · `resume` · `revert` · `section/move` · `shellCommand` · `start` · `turns/list` · `unarchive` · `unsubscribe` |
| Thread sections | 4 | 4 | `threadSection/create` · `threadSection/delete` · `threadSection/list` · `threadSection/update` |
| Turn | 3 | 3 | `interrupt` · `start` · `steer` |
| Account / Login | 12 | 12 | `gatewayOAuth/cancel` · `gatewayOAuth/login` · `gatewayOAuth/read` · `login/cancel` · `login/start` · `logout` · `rateLimitResetCredit/consume` · `rateLimits/read` · `read` · `sendAddCreditsNudgeEmail` · `usage/read` · `workspaceMessages/read` |
| Models | 2 | 2 | `model/list` · `modelProvider/capabilities/read` |
| Review | 1 | 1 | `review/start` |
| Config | 5 | 5 | `config/batchWrite` · `config/mcpServer/reload` · `config/read` · `config/value/write` · `configRequirements/read` |
| Skills | 3 | 3 | `skills/config/write` · `skills/extraRoots/set` · `skills/list` |
| Plugins / Marketplace | 15 | 15 | `marketplace/add` · `marketplace/remove` · `marketplace/upgrade` · `install` · `installed` · `list` · `read` · `reconcile` · `share/checkout` · `share/delete` · `share/list` · `share/save` · `share/updateTargets` · `skill/read` · `uninstall` |
| Apps | 3 | 3 | `installed` · `list` · `read` |
| Filesystem | 9 | 9 | `copy` · `createDirectory` · `getMetadata` · `readDirectory` · `readFile` · `remove` · `unwatch` · `watch` · `writeFile` |
| MCP | 4 | 4 | `mcpServer/oauth/login` · `mcpServer/resource/read` · `mcpServer/tool/call` · `mcpServerStatus/list` |
| Command exec | 4 | 4 | `command/exec` · `command/exec/resize` · `command/exec/terminate` · `command/exec/write` |
| Experimental features | 2 | 2 | `experimentalFeature/enablement/set` · `experimentalFeature/list` |
| Hooks | 1 | 1 | `hooks/list` |
| External agent config | 4 | 4 | `externalAgentConfig/detect` · `externalAgentConfig/import` · `externalAgentConfig/import/readHistories` · `externalAgentConfig/import/recordHistory` |
| Permissions | 1 | 1 | `permissionProfile/list` |
| Windows sandbox | 2 | 2 | `windowsSandbox/readiness` · `windowsSandbox/setupStart` |
| Other | 2 | 5 | `feedback/upload` · `fuzzyFileSearch` · `getAuthStatus` · `getConversationSummary` · `gitDiffToRemote` |

### Server → client

| Kind | Implemented | Declared |
|---|:---:|:---:|
| Notifications (typed decoders) | 62 | 63 |
| Server-initiated requests (handlers) | 9 | 10 |

**Total: 177 of 177 in-scope methods implemented** (5 deliberately not implemented, struck through above and in `gen/whitelist.json` with a reason for each).

Methods upstream marks `#[experimental]` are out of scope by decision R2 and are listed in `gen/not-in-scope.txt`.
<!-- coverage:end -->

### Transport notes

| Transport | Status |
|---|---|
| stdio | ✅ (backed by jrpc2, which does emit the `jsonrpc` field — see Reliability) |
| WebSocket | ✅ keepalive + automatic re-dial |
| HTTP + SSE | 🚧 WIP |

## Reliability

Two behaviours are worth knowing before you build on this SDK, because both are places where
a naive client silently produces wrong results.

### Reconnection is not enough on its own

A transport reconnect restores the **socket**, not the **session**. The app-server treats a
new connection as a brand-new client, so the initialize handshake has to be replayed and every
open thread resumed. Without that, calls keep being issued on a connection the server has no
session for — and they do not fail cleanly; they can appear to succeed against a fresh, empty
session, which is worse.

`WithAutoReconnect()` does this for you. It requires a transport that reports its own
reconnections (`NewReconnectingWS`); `New` returns an error otherwise rather than silently
doing nothing.

```go
client, err := codexgo.New(
    codexgo.WithTransport(wsTransport), // e.g. NewReconnectingWS
    codexgo.WithAutoReconnect(),
)
```

Recovery is reported as events, so a caller can tell a routine blip from a lost session:

| Event | Meaning |
|---|---|
| `sdk/reconnectStarted` | a recovery round began |
| `sdk/sessionRecovered` | which threads came back, and which did not |
| `sdk/reconnectSucceeded` | the handshake and all resumptions completed |
| `sdk/reconnectFailed` | one round failed; retries continue with backoff |
| `sdk/unhandledServerRequest` | the server asked something no handler covered, and what was replied |

These are **synthesised by the SDK**, not wire notifications — upstream has no such methods —
hence the `sdk/` prefix. They arrive on the same `Events()` subscription as real events.

Dropped connections also do not replay notifications: upstream offers no replay, so anything
sent while disconnected is gone. Thread history can be re-read with `thread/turns/list` and
`thread/items/list`.

### Event delivery never blocks the publisher — and it can refuse you

`publish` never waits for a subscriber, because waiting on the shared publish path would stall
every subscriber behind the slowest one. Instead each subscriber has a bounded buffer and a
forwarding goroutine, and if a consumer stops reading for too long its subscription is
**terminated** rather than blocking or silently dropping.

So a consumer must handle termination. Check `Err()`, or watch `Done()`; do not assume
`range sub.C()` ends only when you want it to:

```go
sub := client.Events()
defer sub.Close()

for {
    select {
    case ev, ok := <-sub.C():
        if !ok {
            return // terminated; see sub.Err() for why
        }
        handle(ev)
    case <-sub.Done():
        var lost *codexgo.EventsLostError
        if errors.As(sub.Err(), &lost) {
            log.Printf("events stopped: reason=%s lost=%d", lost.Reason, lost.LostCount)
        }
        return
    }
}
```

`Err()` is the authoritative channel: it is populated even if you never read `C()`. The
terminal `EventsLostEvent` is also written in-band before the channel closes, but only the
out-of-band `Err()`/`Done()` pair is guaranteed not to depend on you draining the buffer.

`Close()` returns immediately, is idempotent, and works for a subscription that never received
an event.

## Policies

Three decisions shape what is and is not implemented. All are enforced mechanically by
`make conformance-strict`.

| # | Policy |
|---|---|
| **R2** | Experimental surface is not implemented. Upstream marks methods `#[experimental]`; they are listed in `gen/not-in-scope.txt`. The SDK also does not declare `experimentalApi` in its handshake by default. |
| **R3** | If upstream removes or renames a method, the SDK migrates rather than keeping a compatibility shim. `gen/unknown-methods.txt` must stay empty. |
| **R4** | A small set of stable methods is deliberately not implemented (legacy v1 approvals, attestation, a few v1 top-level calls). Each has a written reason in `gen/whitelist.json`. Unhandled server requests are still **answered** legally so the session continues, and reported via `sdk/unhandledServerRequest`. |

### Upstream does not use JSON-RPC 2.0

Upstream is explicit (`app-server-protocol/src/rpc.rs`): it neither sends nor expects the
`"jsonrpc": "2.0"` field. The SDK therefore omits it on every transport it controls, and there
is no compatibility switch.

One deliberate exception: the **stdio** transport is backed by `jrpc2`, a strict JSON-RPC 2.0
implementation that both emits and requires the field. Its inbound workaround is documented in
`internal/transport/stdio.go`. Switching to WebSocket avoids it.

## Install

```bash
go get github.com/zealbase/codex-app-server-go
```

```go
import codexgo "github.com/zealbase/codex-app-server-go"
```

**Version:** `v0.2.0` · **Protocol:** Codex app-server **v2** · **Go:** 1.25+

## How to Use

### Local binary (stdio)

```go
cmd := exec.Command("codex", "app-server", "--stdio")
stdout, _ := cmd.StdoutPipe()
stdin, _ := cmd.StdinPipe()
_ = cmd.Start()

client, _ := codexgo.New(codexgo.WithStdioTransport(stdout, stdin))
defer client.Close()

ctx := context.Background()
_, _ = client.Initialize(ctx, codexgo.InitializeParams{
    ClientInfo:   codexgo.ClientInfo{Name: "my-tool", Version: "0.1.0"},
    InitializeCapabilities: codexgo.InitializeCapabilities{ExperimentalAPI: true},
})

thread, _ := client.StartThread(ctx, codexgo.WithThreadModel("claude-opus-4-8"))
defer thread.Close()

result, _ := thread.Run(ctx, "Summarize this repo in one sentence.")
fmt.Println(result.FinalAgentText())
```

### Remote server (WebSocket)

```go
dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
defer cancel()

client, _ := codexgo.New(
    codexgo.WithWSBearerToken("my-api-key"),
    codexgo.WithWSTransport(dialCtx, "ws://codex-server.example.com"),
)
defer client.Close()
thread, _ := client.StartThread(ctx, codexgo.WithThreadModel("claude-opus-4-8"))
```

### Streaming

`RunStreamed` returns a channel of `ThreadEvent`. Each carries the wire method in `Kind` and
the decoded event in `Raw`, so switch on the latter for the typed form:

```go
ch, _ := thread.RunStreamed(ctx, "Explain this codebase.")
for ev := range ch {
    switch e := ev.Raw.(type) {
    case codexgo.ItemAgentMessageDeltaEvent:
        fmt.Print(e.Text)
    case codexgo.ItemCommandExecutionOutputDeltaEvent:
        fmt.Fprint(os.Stderr, e.Output)
    }
}
```

The channel closes when the turn ends. It can also close if this consumer falls far enough
behind, which is why a long-lived reader should check `Err()` — see
[Reliability](#event-delivery-never-blocks-the-publisher--and-it-can-refuse-you).

### Structured output

```go
sub := client.Events(); defer sub.Close()
turn, _ := client.TurnStart(ctx, codexgo.TurnStartParams{
    ThreadID:     thread.ID(),
    Input:        "Return {\"answer\":\"PONG\",\"n\":42} as JSON.",
    OutputSchema: schemaJSON,
})
var out struct{ Answer string; N int }
_, _ = client.WaitForStructuredOutput(ctx, thread.ID(), turn.ID, &out)
```

## Full Protocol Coverage

The table in [What's Implemented](#whats-implemented) is generated from
`gen/method-surface.json` and `gen/implemented-methods.json` and is verified by
`make conformance-strict`, so it cannot drift from reality.

There used to be a second, hand-written coverage table here. It claimed 51% of RPC methods
and 0% of filesystem/MCP — off by more than a hundred methods — because two tables describing
the same machine-checked data will always diverge. It has been removed rather than updated.

For the method-by-method breakdown, see [`gen/conformance-report.md`](gen/conformance-report.md);
for the anchor commit and schema revision, see
[`internal/protocol/schema/version.go`](internal/protocol/schema/version.go).

## Documentation

- [`docs/index.md`](docs/index.md) — guide, transports, SessionThread, wait helpers
- [`docs/api-reference.md`](docs/api-reference.md) — full type & method reference
- [`llms.txt`](llms.txt) / [`llms-full.txt`](llms-full.txt) — machine-readable references

## Examples

Runnable programs under [`examples/`](examples). Each is `package main`, so
`go run ./examples/<name>` works.

| Example | Shows |
|---|---|
| [`simple`](examples/simple) | the basics: find the binary, start a thread, run a prompt |
| [`reconnect-supervisor`](examples/reconnect-supervisor) | `WithAutoReconnect`, and consuming the `sdk/*` recovery events |
| [`approval-over-websocket`](examples/approval-over-websocket) | a `Dispatcher` that allows, denies and answers — and why refusing is not an error |
| [`fs-and-mcp`](examples/fs-and-mcp) | the `fs/*` and `mcpServer*` RPCs, including the connection-scoped `fs/watch` |
| [`streaming-to-sse`](examples/streaming-to-sse) | forwarding a turn to a browser, and handling a subscription that ends on its own |

## Protocol reference

This client targets the **Codex app-server v2** JSON-RPC schema. The canonical schema is
produced by the Codex binary itself:

```bash
codex app-server generate-json-schema --out <dir>
```

A pinned copy ships at
`internal/protocol/schema/codex_app_server_protocol.v2.schemas.json`.
Upstream protocol source: [`openai/codex`](https://github.com/openai/codex) —
`codex-rs/app-server-protocol/`.

## License

Licensed under the **Apache License, Version 2.0**, consistent with upstream Codex.
See [`LICENSE`](LICENSE) for the full text.
