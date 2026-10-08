// approval-over-websocket shows the Dispatcher: the object that answers the server's
// approval, permission, user-input and elicitation requests.
//
// The recurring theme is that *refusing* is a normal protocol answer. Declining a command
// or an elicitation does not end the turn -- the model is told to try something else --
// whereas replying with a JSON-RPC error can. That is why every handler below returns a
// decision rather than an error when it says no.
//
// Run: CODEX_WS_URL=ws://127.0.0.1:1455 go run ./examples/approval-over-websocket
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"

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
		codexgo.WithReconnectingWSTransport(ctx, url),
		codexgo.WithAutoReconnect(),
		codexgo.WithRequestHandler(&codexgo.Dispatcher{
			Exec:        allowCommands{prefixes: []string{"go test", "go build", "git status"}},
			File:        denyFiles{},
			Permissions: denyPermissions{},
			UserInput:   firstOption{},

			// Elicitation is deliberately left nil. An MCP server asking for input is then
			// answered with a decline and the turn continues -- and the SDK reports it via
			// sdk/unhandledServerRequest so you can notice and install a handler.
			//
			// AuthTokens is left nil too, but that one is different: a token request has no
			// decline shape, so the SDK answers with an error rather than fabricating a
			// credential. Install a handler if this client owns the ChatGPT token lifecycle.
		}),
	)
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	defer client.Close()

	go logUnhandled(ctx, client)

	thread, err := client.StartThread(ctx)
	if err != nil {
		log.Fatalf("start thread: %v", err)
	}
	defer thread.Close()

	result, err := thread.Run(ctx, "run the test suite and report the result")
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	log.Printf("turn %s finished with status %s", result.Turn.ID, result.Turn.Status)
}

// allowCommands approves only the commands on its allow-list.
//
// An allow-list is the right shape here because the alternative -- approving everything --
// hands the model unrestricted shell access, and declining is cheap: the model simply tries
// something else.
type allowCommands struct{ prefixes []string }

func (a allowCommands) HandleCommandExecutionApproval(_ context.Context, req codexgo.CommandExecutionApprovalRequest) (codexgo.CommandExecutionApprovalResult, error) {
	for _, p := range a.prefixes {
		if strings.HasPrefix(strings.TrimSpace(req.Command), p) {
			return codexgo.CommandExecutionApprovalResult{
				Decision: codexgo.CommandExecutionApprovalDecisionAccept,
			}, nil
		}
	}
	log.Printf("declining command %q (cwd=%s)", req.Command, req.Cwd)
	return codexgo.CommandExecutionApprovalResult{
		Decision: codexgo.CommandExecutionApprovalDecisionDecline,
	}, nil
}

// denyFiles refuses every file change.
type denyFiles struct{}

func (denyFiles) HandleFileChangeApproval(_ context.Context, req codexgo.FileChangeApprovalRequest) (codexgo.FileChangeApprovalResult, error) {
	log.Printf("declining change to %v", req.FilePaths)
	return codexgo.FileChangeApprovalResult{
		Decision: codexgo.FileChangeApprovalDecisionDecline,
	}, nil
}

// denyPermissions grants nothing.
//
// The response is a *profile*, not a boolean: an empty GrantedPermissionProfile means no
// extra network access and no extra filesystem access. Escalating silently is the one thing
// to avoid here, which is also why the SDK answers with an empty profile when no handler is
// installed at all.
type denyPermissions struct{}

func (denyPermissions) HandlePermissionsApproval(_ context.Context, req codexgo.PermissionsApprovalRequest) (codexgo.PermissionsApprovalResponse, error) {
	log.Printf("declining to widen permissions for %s", req.CWD)
	return codexgo.PermissionsApprovalResponse{
		Permissions: codexgo.GrantedPermissionProfile{},
	}, nil
}

// firstOption answers each question with its first option, or "yes" when there is none.
//
// Note the answer shape: each answer is a *list* of choices, because upstream models a
// question as possibly multi-select.
type firstOption struct{}

func (firstOption) HandleUserInput(_ context.Context, req codexgo.UserInputRequest) (codexgo.UserInputResponse, error) {
	answers := make(map[string]codexgo.UserInputAnswer, len(req.Questions))
	for _, q := range req.Questions {
		text := "yes"
		if len(q.Options) > 0 {
			text = q.Options[0].Label
		}
		answers[q.ID] = codexgo.UserInputAnswer{Answers: []string{text}}
	}
	return codexgo.UserInputResponse{Answers: answers}, nil
}

// logUnhandled surfaces the requests the dispatcher answered on our behalf.
//
// Without this, a nil handler would mean those requests were refused silently and the only
// clue would be a model that keeps "choosing" not to do things.
func logUnhandled(ctx context.Context, client *codexgo.Client) {
	sub := client.Events()
	defer sub.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub.C():
			if !ok {
				return
			}
			if report, isReport := ev.Value.(codexgo.UnhandledServerRequestEvent); isReport {
				log.Printf("unhandled server request: %s -> %s (%s)",
					report.Method, report.Action, report.Reason)
			}
		case <-sub.Done():
			return
		}
	}
}
