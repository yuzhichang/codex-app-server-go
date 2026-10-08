// reconnect-supervisor shows what WithAutoReconnect actually does, and how to watch it.
//
// The point worth internalising: a transport reconnect restores the *socket*, not the
// *session*. The app-server treats a new connection as a brand-new client, so the
// initialize handshake has to be replayed and every open thread resumed. Without that the
// SDK keeps calling on a connection the server has no session for -- and those calls do
// not fail cleanly, they can appear to succeed against a fresh, empty session.
//
// Run: CODEX_WS_URL=ws://127.0.0.1:1455 go run ./examples/reconnect-supervisor
// Then kill the app-server and watch the log.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
)

func main() {
	url := os.Getenv("CODEX_WS_URL")
	if url == "" {
		log.Fatal("set CODEX_WS_URL to a codex app-server WebSocket endpoint, e.g. ws://127.0.0.1:1455")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client, err := codexgo.New(
		// The reconnecting transport is what reports each re-dial. Pairing it with
		// WithAutoReconnect is what turns "the socket came back" into "the session came
		// back"; New() returns an error if given a transport that cannot reconnect, rather
		// than silently doing nothing.
		codexgo.WithReconnectingWSTransport(ctx, url),
		codexgo.WithAutoReconnect(),
	)
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	defer client.Close()

	thread, err := client.StartThread(ctx)
	if err != nil {
		log.Fatalf("start thread: %v", err)
	}
	defer thread.Close()

	go watchRecovery(ctx, client)

	// A long-lived loop. Interrupt the network or restart the app-server; the next turn
	// should go through rather than failing with a stale-connection error.
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := thread.Run(ctx, "report the current time"); err != nil {
				log.Printf("turn failed: %v", err)
			}
		}
	}
}

// watchRecovery consumes the supervisor's events.
//
// These are synthesised by the SDK, not wire notifications -- upstream has no such methods
// -- which is why they carry the `sdk/` prefix. They arrive on the same subscription as
// real events so one consumer loop sees everything.
func watchRecovery(ctx context.Context, client *codexgo.Client) {
	sub := client.Events()
	defer sub.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub.C():
			if !ok {
				reportStop(sub)
				return
			}
			switch e := ev.Value.(type) {
			case codexgo.ReconnectStartedEvent:
				log.Printf("reconnect: attempt %d started", e.Attempt)
			case codexgo.SessionRecoveredEvent:
				log.Printf("reconnect: resumed %d thread(s), %d failed: %v",
					len(e.ThreadsResumed), len(e.ThreadsFailed), e.ThreadsFailed)
			case codexgo.ReconnectSucceededEvent:
				log.Printf("reconnect: session restored on attempt %d (%d thread(s))",
					e.Attempt, e.ThreadsResumed)
			case codexgo.ReconnectFailedEvent:
				log.Printf("reconnect: attempt %d failed, retrying: %v", e.Attempt, e.Err)
			case codexgo.UnhandledServerRequestEvent:
				// Something asked for input that no handler covered. The session continued
				// (the SDK answered legally), but you probably want to handle it.
				log.Printf("server asked %q; answered %s (%s)", e.Method, e.Action, e.Reason)
			}
		case <-sub.Done():
			reportStop(sub)
			return
		}
	}
}

func reportStop(sub *codexgo.EventSubscription) {
	var lost *codexgo.EventsLostError
	if errors.As(sub.Err(), &lost) {
		log.Printf("event stream stopped: reason=%s lost=%d", lost.Reason, lost.LostCount)
	}
}
