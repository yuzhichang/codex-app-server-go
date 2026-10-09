# Reconnection and event delivery

This document covers the two behaviours that most often surprise callers of this SDK:
what "reconnect" actually restores, and what the event subscription guarantees. Both are
places where a naive client silently produces wrong results rather than failing loudly.

## A transport reconnect restores the socket, not the session

The app-server has **no protocol-level session resume and no client event replay**. A new
connection is a brand-new client: the initialize handshake must be replayed and every open
thread resumed. Calls issued on a reconnected socket without replaying the handshake do not
fail cleanly — they can appear to succeed against a fresh, empty session, which is worse than
an error.

Enable the supervisor with `WithAutoReconnect()`:

```go
client, err := codexgo.New(
    codexgo.WithTransport(wsTransport), // e.g. NewReconnectingWS
    codexgo.WithAutoReconnect(),
)
```

`WithAutoReconnect` requires a transport that reports its own reconnects
(`NewReconnectingWS` / `WithReconnectingWSTransport`). `New` returns an error otherwise rather
than silently doing nothing. On each reconnect the supervisor:

1. re-runs `initialize` + `initialized`;
2. calls `thread/resume` for every tracked thread;
3. backfills history for the resumed threads (see below).

Recovery is reported as **synthetic `sdk/` events** on the normal `Events()` subscription
(upstream has no such methods, so they cannot collide with real notifications):

| Event | Meaning |
|---|---|
| `sdk/reconnectStarted` | a recovery round began |
| `sdk/sessionRecovered` | which threads came back, and which did not (`ThreadsResumed` / `ThreadsFailed`) |
| `sdk/reconnectSucceeded` | the handshake and all resumptions completed (`ThreadsResumed` / `ThreadsFailed`) |
| `sdk/reconnectFailed` | one round failed (`Err`, `Attempt`); retries continue with backoff |
| `sdk/unhandledServerRequest` | the server asked something no handler covered, and what was replied |

`Reconnects()` at the transport level fires once per successful re-dial (coalesced, capacity 1);
the supervisor consumes it. "Re-dial" is not "session recovery" — that distinction is the whole
point.

## Missed notifications are not replayed; history is backfilled instead

Anything sent while the connection was down is gone. The SDK does **not** re-emit those
notifications as wire-shaped events, because a consumer that already saw part of a turn would
double-count them. Instead each resumed thread is backfilled from `thread/turns/list` and
delivered as `sdk/sessionBackfilled`:

```go
case codexgo.SessionBackfilledEvent:
    // History, not a replay: merge by turn id rather than appending.
    for _, turn := range e.Turns {
        reconcile(e.ThreadID, turn)
    }
```

Properties worth relying on:

- **Merge by turn id.** It is authoritative history, not a replay of missed notifications.
- **Bounded** to the most recent turns per thread (20 by default). `Truncated` signals older
  turns exist; fetch them with `thread/turns/list`.
- **Configurable**: `WithSessionBackfill(n)` changes the bound; `WithSessionBackfill(0)` disables it.
- **Only resumed threads** are backfilled. A thread that could not be resumed is reported under
  `ThreadsFailed` on `sdk/sessionRecovered`.

Because upstream provides no replay, the effective contract is **at-least-once with
application-level idempotency**: reconcile by identity, do not assume exactly-once delivery.

## Event delivery never blocks the publisher

`publish` never waits for a subscriber: waiting on the shared publish path would stall every
subscriber behind the slowest one. Each subscriber instead owns a bounded buffer plus a
forwarding goroutine, and a consumer that stops reading past the bound has its subscription
**terminated** rather than blocking the producer or dropping events silently.

So a consumer must handle termination. Check `Err()`, or watch `Done()` — do not assume
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

`Err()` / `Done()` are authoritative and out-of-band: they are populated even if you never read
`C()`. The terminal `EventsLostEvent` is also written in-band into a reserved slot before the
channel closes, but only the `Err()` / `Done()` pair is guaranteed not to depend on you draining
the buffer. `EventsLostError` carries `GapFrom` / `GapTo`, the time window of events still
queued when the subscription terminated. `Close()` is idempotent, safe to call concurrently, and
does not block; after it returns, the forwarding goroutine exits and closes `out` / `done`.

## Approval handlers are bounded too

A handler that never returns would hang the turn forever. Set `Dispatcher.ApprovalTimeout` to
bound it; on expiry the SDK replies with the **same refusal it gives when no handler is
configured** and reports the request as `timedOut`. A timeout can never grant anything — that
invariant is pinned by a test.

```go
dispatcher := &codexgo.Dispatcher{
    Exec:            myApprovalHandler,
    ApprovalTimeout: 2 * time.Minute,
}
```

The zero value waits indefinitely, which is right when approvals come from a human; set a
timeout when they come from an automated reviewer.

## Known transport caveats

- **stdio is backed by `jrpc2`**, which emits and requires the `jsonrpc` field and parses the
  envelope itself. Padding the inbound stream (`versionFixerReader`) is required, and the
  `emittedAtMs` sibling field cannot be recovered, so `EmittedAtMs` is 0 on stdio. WebSocket and
  HTTP are fully covered.
- **HTTP + SSE is work-in-progress**: the app-server is WebSocket-only, so `WithHTTPTransport`
  requires the `ws-http-bridge` sidecar in front of it (else requests 405).
