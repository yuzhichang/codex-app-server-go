package real_test

import (
	"context"
	"strings"
	"testing"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
)

func TestReal_SessionThreadRun(t *testing.T) {
	skipIfNoEndpoint(t)
	client := newRealClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	thread, err := client.StartThread(ctx, codexgo.WithThreadModel(realModel()))
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	defer thread.Close()

	result, err := thread.Run(ctx, "Reply with only the single word: PONG")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	resp := result.FinalAgentText()
	if resp == "" {
		// Fall back to server read in case items arrived after Run returned.
		var wErr error
		resp, wErr = client.WaitForFinalAgentMessage(ctx, thread.ID(), result.Turn.ID)
		if wErr != nil {
			t.Fatalf("WaitForFinalAgentMessage: %v", wErr)
		}
	}
	if !strings.Contains(strings.ToUpper(resp), "PONG") {
		t.Fatalf("response %q does not contain PONG", resp)
	}
}

func TestReal_TurnUsage(t *testing.T) {
	skipIfNoEndpoint(t)
	client := newRealClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	thread, err := client.StartThread(ctx, codexgo.WithThreadModel(realModel()))
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	defer thread.Close()

	result, err := thread.Run(ctx, "say ok")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Wait for the turn to be fully committed before reading usage.
	if _, err := client.WaitForFinalAgentMessage(ctx, thread.ID(), result.Turn.ID); err != nil {
		t.Fatalf("WaitForFinalAgentMessage: %v", err)
	}
	if _, err := client.TurnRead(ctx, thread.ID(), result.Turn.ID); err != nil {
		t.Fatalf("TurnRead: %v", err)
	}
	// Usage is not part of the turn read response: upstream carries it on
	// thread/tokenUsage/updated, which TurnResult.Usage reflects.
	if result.Usage == nil {
		t.Skip("server did not send thread/tokenUsage/updated; skipping usage assertion")
	}
	if result.Usage.Total.TotalTokens <= 0 {
		t.Fatalf("Usage.Total.TotalTokens = %d, want > 0", result.Usage.Total.TotalTokens)
	}
}

func TestReal_MultiTurn(t *testing.T) {
	skipIfNoEndpoint(t)
	client := newRealClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	thread, err := client.StartThread(ctx, codexgo.WithThreadModel(realModel()))
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	defer thread.Close()

	if _, err := thread.Run(ctx, "Remember the number 42."); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if _, err := thread.Run(ctx, "What number did I ask you to remember?"); err != nil {
		t.Fatalf("second Run: %v", err)
	}
}

func TestReal_StartThreadWithInitialInput(t *testing.T) {
	skipIfNoEndpoint(t)
	client := newRealClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	thread, err := client.StartThread(ctx,
		codexgo.WithThreadModel(realModel()),
		codexgo.WithInitialInput("say only: READY"),
	)
	if err != nil {
		t.Fatalf("StartThread with initial input: %v", err)
	}
	defer thread.Close()

	if thread.ID() == "" {
		t.Fatal("StartThread returned thread with empty ID")
	}
}
