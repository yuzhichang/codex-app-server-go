// streaming-to-sse forwards an agent turn to a browser as Server-Sent Events.
//
// It exists mostly to show the one thing that trips people up about this SDK's event
// stream: a subscription can end on its own. The SDK never blocks a publisher for a slow
// consumer -- that would stall every subscriber behind the slowest one -- so instead each
// subscriber has a bounded buffer and is *terminated* if its consumer stops keeping up.
// `range` over the channel finishing is therefore normal, and the reason is on Err().
//
// Run: go run ./examples/streaming-to-sse   then open http://localhost:8080/events
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
)

func main() {
	bin, err := codexgo.FindBinary()
	if err != nil {
		log.Fatalf("codex binary not found: %v (set CODEX_BIN or install codex)", err)
	}

	client, err := codexgo.New(codexgo.WithStdioProcess(bin, "app-server"))
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	thread, err := client.StartThread(ctx)
	if err != nil {
		log.Fatalf("start thread: %v", err)
	}
	defer thread.Close()

	http.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		streamTurn(w, r, thread)
	})
	log.Println("listening on http://localhost:8080/events")
	log.Fatal(http.ListenAndServe("localhost:8080", nil))
}

func streamTurn(w http.ResponseWriter, r *http.Request, thread *codexgo.SessionThread) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, err := thread.RunStreamed(r.Context(), "summarise what this repository does")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// RunStreamed's channel is closed by the SDK when the turn ends -- or when this
	// consumer falls too far behind. Both are normal shutdowns; the second is why the
	// caller must not treat a closed channel as an error in itself.
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				emit(w, flusher, "end", map[string]string{"reason": "stream closed"})
				return
			}
			switch e := ev.Raw.(type) {
			case codexgo.ItemAgentMessageDeltaEvent:
				if e.Text == "" {
					continue
				}
				emit(w, flusher, "delta", map[string]string{"text": e.Text})
			case codexgo.ItemCommandExecutionOutputDeltaEvent:
				emit(w, flusher, "output", map[string]string{"stream": e.Stream, "text": e.Output})
			case codexgo.TurnCompletedEvent:
				emit(w, flusher, "done", map[string]string{"kind": ev.Kind})
				return
			}
		}
	}
}

// consumeEvents is the pattern to copy when reading a subscription directly rather than a
// turn channel: check Err(), or watch Done(). Do not assume the channel only closes when
// you close it.
func consumeEvents(ctx context.Context, sub *codexgo.EventSubscription) {
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-sub.C():
			if !ok {
				var lost *codexgo.EventsLostError
				if errors.As(sub.Err(), &lost) {
					log.Printf("subscription stopped: reason=%s lost=%d", lost.Reason, lost.LostCount)
				}
				return
			}
		case <-sub.Done():
			// Out-of-band: populated even if we never read C().
			var lost *codexgo.EventsLostError
			if errors.As(sub.Err(), &lost) {
				log.Printf("subscription stopped: reason=%s lost=%d", lost.Reason, lost.LostCount)
			}
			return
		}
	}
}

// emit writes one SSE frame, keeping the payload valid JSON.
func emit(w http.ResponseWriter, f http.Flusher, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	f.Flush()
}
