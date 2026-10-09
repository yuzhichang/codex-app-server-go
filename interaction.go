package codexgo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
)

type (
	CommandExecutionApprovalDecision    = protocol.CommandExecutionApprovalDecision
	CommandExecutionApprovalRequest     = protocol.CommandExecutionApprovalRequest
	CommandExecutionApprovalResult      = protocol.CommandExecutionApprovalResponse
	FileChangeApprovalRequest           = protocol.FileChangeApprovalRequest
	FileChangeApprovalResult            = protocol.FileChangeApprovalResponse
	PermissionsApprovalRequest          = protocol.PermissionsApprovalRequest
	PermissionsApprovalResponse         = protocol.PermissionsApprovalResponse
	GrantedPermissionProfile            = protocol.GrantedPermissionProfile
	PermissionsApprovalResult           = protocol.PermissionsApprovalResponse
	UserInputQuestion                   = protocol.UserInputQuestion
	UserInputOption                     = protocol.UserInputOption
	UserInputRequest                    = protocol.UserInputRequest
	UserInputResponse                   = protocol.UserInputResponse
	UserInputAnswer                     = protocol.UserInputAnswer
	UserInputResult                     = protocol.UserInputResult
	ServerRequest                       = protocol.ServerRequest
	ServerResponse                      = protocol.ServerResponse
	McpServerElicitationRequestParams   = protocol.McpServerElicitationRequestParams
	McpServerElicitationRequestResponse = protocol.McpServerElicitationRequestResponse
	McpServerElicitationAction          = protocol.McpServerElicitationAction
	McpServerElicitationMode            = protocol.McpServerElicitationMode
	ChatgptAuthTokensRefreshParams      = protocol.ChatgptAuthTokensRefreshParams
	ChatgptAuthTokensRefreshResponse    = protocol.ChatgptAuthTokensRefreshResponse
	ChatgptAuthTokensRefreshReason      = protocol.ChatgptAuthTokensRefreshReason
)

const (
	CommandExecutionApprovalDecisionAccept                        = "accept"
	CommandExecutionApprovalDecisionAcceptForSession              = "acceptForSession"
	CommandExecutionApprovalDecisionAcceptWithExecPolicyAmendment = "acceptWithExecpolicyAmendment"
	CommandExecutionApprovalDecisionApplyNetworkPolicyAmendment   = "applyNetworkPolicyAmendment"
	CommandExecutionApprovalDecisionDecline                       = "decline"
	CommandExecutionApprovalDecisionCancel                        = "cancel"

	FileChangeApprovalDecisionAccept           = "accept"
	FileChangeApprovalDecisionAcceptForSession = "acceptForSession"
	FileChangeApprovalDecisionDecline          = "decline"
	FileChangeApprovalDecisionCancel           = "cancel"
)

type ApprovalHandler interface {
	HandleCommandExecutionApproval(context.Context, CommandExecutionApprovalRequest) (CommandExecutionApprovalResult, error)
	HandleFileChangeApproval(context.Context, FileChangeApprovalRequest) (FileChangeApprovalResult, error)
	HandlePermissionsApproval(context.Context, PermissionsApprovalRequest) (PermissionsApprovalResult, error)
	HandleUserInputRequest(context.Context, UserInputRequest) (UserInputResult, error)
}

type RequestHandler interface {
	HandleServerRequest(context.Context, ServerRequest) (ServerResponse, error)
}

type RequestHandlerFunc func(context.Context, ServerRequest) (ServerResponse, error)

func (f RequestHandlerFunc) HandleServerRequest(ctx context.Context, req ServerRequest) (ServerResponse, error) {
	return f(ctx, req)
}

type approvalHandlerAdapter struct {
	handler ApprovalHandler
}

func (a *approvalHandlerAdapter) HandleServerRequest(ctx context.Context, req ServerRequest) (ServerResponse, error) {
	decoded, err := protocol.DecodeServerRequest(req.Method, req.Params)
	if err != nil {
		return ServerResponse{}, err
	}

	switch v := decoded.(type) {
	case CommandExecutionApprovalRequest:
		result, err := a.handler.HandleCommandExecutionApproval(ctx, v)
		if err != nil {
			return ServerResponse{}, err
		}
		return serverResponseFrom(result)
	case FileChangeApprovalRequest:
		result, err := a.handler.HandleFileChangeApproval(ctx, v)
		if err != nil {
			return ServerResponse{}, err
		}
		return serverResponseFrom(result)
	case PermissionsApprovalRequest:
		result, err := a.handler.HandlePermissionsApproval(ctx, v)
		if err != nil {
			return ServerResponse{}, err
		}
		return serverResponseFrom(result)
	case UserInputRequest:
		result, err := a.handler.HandleUserInputRequest(ctx, v)
		if err != nil {
			return ServerResponse{}, err
		}
		return serverResponseFrom(result)
	default:
		return ServerResponse{}, protocol.ErrUnsupportedServerRequest
	}
}

func serverResponseFrom(v any) (ServerResponse, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return ServerResponse{}, err
	}
	return ServerResponse{Result: data}, nil
}

// ElicitationHandler handles `mcpServer/elicitation/request`: an MCP server asking the
// client to collect user input.
//
// This replaces the former MCPApprovalHandler, which answered the removed
// `item/mcp/requestApproval` method (plan T1.5 / decision D3).
type ElicitationHandler interface {
	HandleElicitation(ctx context.Context, req McpServerElicitationRequestParams) (McpServerElicitationRequestResponse, error)
}

// AuthTokensHandler mints a fresh ChatGPT access token when the server asks for one via
// `account/chatgptAuthTokens/refresh`.
//
// This is only reached by clients that own the ChatGPT token lifecycle; the SDK's default
// handshake does not claim to. Install a handler with Dispatcher.AuthTokens.
type AuthTokensHandler interface {
	HandleAuthTokensRefresh(ctx context.Context, req ChatgptAuthTokensRefreshParams) (ChatgptAuthTokensRefreshResponse, error)
}

// PermissionsApprovalHandler handles `item/permissions/requestApproval`: the server asking
// to widen the sandbox for a specific request.
type PermissionsApprovalHandler interface {
	HandlePermissionsApproval(ctx context.Context, req PermissionsApprovalRequest) (PermissionsApprovalResponse, error)
}

// UserInputHandler handles `item/tool/requestUserInput`: the server asking the user a set of
// questions on a tool's behalf.
type UserInputHandler interface {
	HandleUserInput(ctx context.Context, req UserInputRequest) (UserInputResponse, error)
}

// ExecApprovalHandler handles command-execution approval requests.
type ExecApprovalHandler interface {
	HandleCommandExecutionApproval(context.Context, CommandExecutionApprovalRequest) (CommandExecutionApprovalResult, error)
}

// FileApprovalHandler handles file-change approval requests.
type FileApprovalHandler interface {
	HandleFileChangeApproval(context.Context, FileChangeApprovalRequest) (FileChangeApprovalResult, error)
}

// DynamicToolHandler handles dynamic tool call requests from the server.
type DynamicToolHandler interface {
	HandleDynamicToolCall(ctx context.Context, req DynamicToolCallRequest) (DynamicToolCallResult, error)
}

// DynamicToolCallRequest is the payload for an "item/tool/call" server-initiated request.
type DynamicToolCallRequest struct {
	ToolName  string          `json:"toolName"`
	ToolInput json.RawMessage `json:"input"`
	ThreadID  string          `json:"threadId"`
	TurnID    string          `json:"turnId"`
	ItemID    string          `json:"itemId"`
}

// DynamicToolCallResult is the response to a DynamicToolCallRequest.
type DynamicToolCallResult struct {
	Content []json.RawMessage `json:"content"`
}

// Dispatcher routes incoming server requests to specialised handlers by method.
// Any method not handled by a registered handler is denied with a default response.
//
// Dispatcher implements RequestHandler so it can be passed to WithRequestHandler.
type Dispatcher struct {
	// Exec handles "item/commandExecution/requestApproval" requests.
	// If nil, such requests are denied.
	Exec ExecApprovalHandler

	// File handles "item/fileChange/requestApproval" requests.
	// If nil, such requests are denied.
	File FileApprovalHandler

	// DynamicTool handles "item/tool/call" server-initiated requests.
	// If nil, dynamic tool calls are answered with an empty content array.
	DynamicTool DynamicToolHandler

	// Permissions handles "item/permissions/requestApproval" requests.
	// If nil, the SDK grants nothing (the session continues; decision R9).
	Permissions PermissionsApprovalHandler

	// UserInput handles "item/tool/requestUserInput" requests.
	// If nil, the SDK answers with no answers (the session continues; decision R9).
	UserInput UserInputHandler

	// Elicitation handles "mcpServer/elicitation/request" requests.
	// If nil, such requests are declined (the session continues; decision R9).
	Elicitation ElicitationHandler

	// AuthTokens handles "account/chatgptAuthTokens/refresh" requests.
	//
	// If nil, the request is answered with an error. This is the one deliberate exception
	// to decision R9 ("never terminate the session for an unhandled request"): unlike an
	// approval or an elicitation, this request has no decline shape. The only alternative
	// would be to answer with an empty access token, which the server would then send as a
	// credential -- worse than a clear failure.
	AuthTokens AuthTokensHandler

	// Fallback is consulted for any request method not matched by the above.
	// If nil, unmatched requests are answered with JSON-RPC -32601 (method not found).
	Fallback RequestHandler

	// ApprovalTimeout bounds how long a configured handler may take to answer a
	// server-initiated request before the SDK answers on its behalf.
	//
	// Zero (the default) waits indefinitely, which is the right default for an application
	// whose approvals come from a human. It is worth setting when approvals come from an
	// automated reviewer: a handler that never returns otherwise hangs the turn forever, and
	// a hang is worse than a refusal the server can act on.
	//
	// On timeout the SDK replies with the SAME answer it would have given with no handler
	// configured -- always a refusal -- and reports it as `timedOut`. A timeout can therefore
	// never grant anything.
	ApprovalTimeout time.Duration

	// OnUnhandled is called whenever a request reaches no configured handler, so the
	// application can learn that the server asked it something and what was replied instead.
	//
	// The client installs this automatically when it is given a *Dispatcher, publishing
	// sdk/unhandledServerRequest on Events(). It may also be set directly by callers that
	// construct a Dispatcher themselves.
	OnUnhandled UnhandledServerRequestFunc
}

// UnhandledServerRequestFunc receives a report for each server request the dispatcher could
// not route to a configured handler.
type UnhandledServerRequestFunc func(UnhandledServerRequestEvent)

// HandleServerRequest implements RequestHandler.
func (d *Dispatcher) HandleServerRequest(ctx context.Context, req ServerRequest) (ServerResponse, error) {
	switch req.Method {
	// Legacy v1 approvals. Not implemented as features (decision R4), but they must still be
	// answered with a *valid* decision: a JSON-RPC error can end the turn, whereas a `denied`
	// decision lets the session continue and try something else.
	case protocol.MethodApplyPatchApproval:
		d.notifyUnhandled(req, "declined", "applyPatchApproval is not implemented (decision R4)")
		return serverResponseFrom(protocol.ApplyPatchApprovalResponse{Decision: protocol.DenyReview()})

	case protocol.MethodExecCommandApproval:
		d.notifyUnhandled(req, "declined", "execCommandApproval is not implemented (decision R4)")
		return serverResponseFrom(protocol.ExecCommandApprovalResponse{Decision: protocol.DenyReview()})

	case protocol.MethodItemPermissionsRequestApproval:
		if d.Permissions == nil {
			// Grant nothing rather than erroring: an empty GrantedPermissionProfile is a
			// valid reply (upstream's own test deserializes `{"permissions":{}}`), and
			// escalating permissions on the caller's behalf would be far worse than
			// refusing.
			d.notifyUnhandled(req, "declined", "no PermissionsApprovalHandler configured")
			return serverResponseFrom(protocol.DenyPermissions())
		}
		var r PermissionsApprovalRequest
		if err := json.Unmarshal(req.Params, &r); err != nil {
			return ServerResponse{}, err
		}
		result, timedOut, err := runBounded(ctx, d.ApprovalTimeout, func(hctx context.Context) (PermissionsApprovalResult, error) {
			return d.Permissions.HandlePermissionsApproval(hctx, r)
		})
		if timedOut {
			// Same answer as no handler configured: a timeout can never grant anything.
			d.notifyUnhandled(req, "timedOut", "handler exceeded ApprovalTimeout")
			return serverResponseFrom(protocol.DenyPermissions())
		}
		if err != nil {
			return ServerResponse{}, err
		}
		return serverResponseFrom(result)

	case protocol.MethodItemToolRequestUserInput:
		if d.UserInput == nil {
			// `answers` is required upstream, so refusal is an empty map rather than an
			// absent field.
			d.notifyUnhandled(req, "declined", "no UserInputHandler configured")
			return serverResponseFrom(protocol.DeclineUserInput())
		}
		var r UserInputRequest
		if err := json.Unmarshal(req.Params, &r); err != nil {
			return ServerResponse{}, err
		}
		result, timedOut, err := runBounded(ctx, d.ApprovalTimeout, func(hctx context.Context) (UserInputResult, error) {
			return d.UserInput.HandleUserInput(hctx, r)
		})
		if timedOut {
			// Same answer as no handler configured: a timeout can never grant anything.
			d.notifyUnhandled(req, "timedOut", "handler exceeded ApprovalTimeout")
			return serverResponseFrom(protocol.DeclineUserInput())
		}
		if err != nil {
			return ServerResponse{}, err
		}
		return serverResponseFrom(result)

	case protocol.MethodItemCommandExecutionRequestApproval:
		if d.Exec == nil {
			d.notifyUnhandled(req, "declined", "no ExecApprovalHandler configured")
			return serverResponseFrom(CommandExecutionApprovalResult{Decision: CommandExecutionApprovalDecisionDecline})
		}
		var r CommandExecutionApprovalRequest
		if err := json.Unmarshal(req.Params, &r); err != nil {
			return ServerResponse{}, err
		}
		result, timedOut, err := runBounded(ctx, d.ApprovalTimeout, func(hctx context.Context) (CommandExecutionApprovalResult, error) {
			return d.Exec.HandleCommandExecutionApproval(hctx, r)
		})
		if timedOut {
			// Same answer as no handler configured: a timeout can never grant anything.
			d.notifyUnhandled(req, "timedOut", "handler exceeded ApprovalTimeout")
			return serverResponseFrom(CommandExecutionApprovalResult{Decision: CommandExecutionApprovalDecisionDecline})
		}
		if err != nil {
			return ServerResponse{}, err
		}
		return serverResponseFrom(result)

	case protocol.MethodItemFileChangeRequestApproval:
		if d.File == nil {
			d.notifyUnhandled(req, "declined", "no FileApprovalHandler configured")
			return serverResponseFrom(FileChangeApprovalResult{Decision: FileChangeApprovalDecisionDecline})
		}
		var r FileChangeApprovalRequest
		if err := json.Unmarshal(req.Params, &r); err != nil {
			return ServerResponse{}, err
		}
		result, timedOut, err := runBounded(ctx, d.ApprovalTimeout, func(hctx context.Context) (FileChangeApprovalResult, error) {
			return d.File.HandleFileChangeApproval(hctx, r)
		})
		if timedOut {
			// Same answer as no handler configured: a timeout can never grant anything.
			d.notifyUnhandled(req, "timedOut", "handler exceeded ApprovalTimeout")
			return serverResponseFrom(FileChangeApprovalResult{Decision: FileChangeApprovalDecisionDecline})
		}
		if err != nil {
			return ServerResponse{}, err
		}
		return serverResponseFrom(result)

	case protocol.MethodItemToolCall:
		if d.DynamicTool == nil {
			d.notifyUnhandled(req, "declined", "no DynamicToolHandler configured")
			return serverResponseFrom(DynamicToolCallResult{Content: []json.RawMessage{}})
		}
		var r DynamicToolCallRequest
		if err := json.Unmarshal(req.Params, &r); err != nil {
			return ServerResponse{}, err
		}
		result, err := d.DynamicTool.HandleDynamicToolCall(ctx, r)
		if err != nil {
			return ServerResponse{}, err
		}
		return serverResponseFrom(result)

	case protocol.MethodMcpServerElicitationRequest:
		if d.Elicitation == nil {
			// Decline rather than error: refusing an elicitation is a valid protocol
			// answer and must not terminate the turn (decision R9).
			d.notifyUnhandled(req, "declined", "no ElicitationHandler configured")
			return serverResponseFrom(protocol.DeclineElicitation())
		}
		var r McpServerElicitationRequestParams
		if err := json.Unmarshal(req.Params, &r); err != nil {
			return ServerResponse{}, err
		}
		result, timedOut, err := runBounded(ctx, d.ApprovalTimeout, func(hctx context.Context) (McpServerElicitationRequestResponse, error) {
			return d.Elicitation.HandleElicitation(hctx, r)
		})
		if timedOut {
			// Same answer as no handler configured: a timeout can never grant anything.
			d.notifyUnhandled(req, "timedOut", "handler exceeded ApprovalTimeout")
			return serverResponseFrom(protocol.DeclineElicitation())
		}
		if err != nil {
			return ServerResponse{}, err
		}
		return serverResponseFrom(result)

	case protocol.MethodChatgptAuthTokensRefresh:
		if d.AuthTokens == nil {
			// No decline shape exists for a token request; see the field comment above.
			// This is the one case in R9 where the honest answer is an error rather than a
			// protocol-level refusal, so it is reported as "refused" rather than "declined".
			d.notifyUnhandled(req, "refused", "no AuthTokensHandler configured and a token request has no decline shape")
			return ServerResponse{}, protocol.ErrUnsupportedServerRequest
		}
		var r ChatgptAuthTokensRefreshParams
		if err := json.Unmarshal(req.Params, &r); err != nil {
			return ServerResponse{}, err
		}
		result, err := d.AuthTokens.HandleAuthTokensRefresh(ctx, r)
		if err != nil {
			return ServerResponse{}, err
		}
		return serverResponseFrom(result)

	default:
		if d.Fallback != nil {
			return d.Fallback.HandleServerRequest(ctx, req)
		}
		// -32601 rather than an error the server cannot classify: "I do not know this
		// method" is actionable (it can stop asking), whereas a generic failure may be
		// treated as a broken session (decision R9).
		d.notifyUnhandled(req, "methodNotFound", "no handler for this method")
		return ServerResponse{}, &protocol.MethodNotFoundError{Method: req.Method}
	}
}

// notifyUnhandled records that the SDK answered a server request on the caller's behalf
// because no handler was configured for it.
//
// The dispatcher is constructed by the caller and has no back-reference to the client, so it
// reports through a callback the client installs on it.
// runBounded runs a configured approval / user-input / elicitation handler under
// ApprovalTimeout.
//
// The dispatcher -- not the handler -- owns the deadline. It used to call the handler
// synchronously and only treat a returned context.DeadlineExceeded as a timeout, which has
// two holes: a handler that ignores its context is never bounded at all, and one that
// returns "accept" after the deadline (with a nil error) still grants the request. So the
// handler runs on its own goroutine and the dispatcher returns the timeout decision the
// moment the deadline fires, discarding whatever the handler eventually says. The buffered
// channel lets a late handler finish and exit rather than leak.
func runBounded[T any](ctx context.Context, timeout time.Duration, fn func(context.Context) (T, error)) (res T, timedOut bool, err error) {
	if timeout <= 0 {
		r, e := fn(ctx)
		return r, false, e
	}
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type outcome struct {
		res T
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, e := fn(hctx)
		ch <- outcome{r, e}
	}()
	select {
	case o := <-ch:
		// The handler finished on its own; if it reports that it exceeded the deadline, that
		// still counts as a timeout.
		if errors.Is(o.err, context.DeadlineExceeded) {
			return res, true, nil
		}
		return o.res, false, o.err
	case <-hctx.Done():
		return res, true, nil
	}
}

func (d *Dispatcher) notifyUnhandled(req ServerRequest, action, reason string) {
	if d == nil || d.OnUnhandled == nil {
		return
	}
	ev := UnhandledServerRequestEvent{Method: req.Method, Action: action, Reason: reason}
	// Thread/turn ids are best-effort: most payloads carry them, but not all request types do.
	if len(req.Params) > 0 {
		var probe struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
		}
		if err := json.Unmarshal(req.Params, &probe); err == nil {
			ev.ThreadID, ev.TurnID = probe.ThreadID, probe.TurnID
		}
	}
	d.OnUnhandled(ev)
}

// TurnInput is a single typed input item for a turn or steer request. Construct
// values with TextInput, ImageInput, LocalImageInput, SkillInput, or
// MentionInput. Each serializes to the multi-part wire shape the app-server
// expects.
// TextInputs wraps a plain prompt as the input array upstream requires.
//
// A bare string is not a valid value: upstream expects [{"type":"text","text":...}].
func TextInputs(text string) []UserInput {
	if text == "" {
		return nil
	}
	return []UserInput{{Type: UserInputTypeText, Text: text}}
}

// TurnInput is the former map-based input type. Deprecated: UserInput is typed, so a
// malformed element is a compile error instead of a payload the server rejects.
type TurnInput = UserInput

// TextInput wraps a plain text fragment: {"type":"text","text":...}.
func TextInput(text string) UserInput {
	return UserInput{Type: UserInputTypeText, Text: text}
}

// ImageInput references an image the server can fetch by URL: {"type":"image","url":...}.
//
// The former mediaType parameter is gone: `mediaType` is not a field of any UserInput variant,
// so it was sent as an unrecognised extra and ignored. The server derives the type from the
// response.
func ImageInput(url string) UserInput {
	return UserInput{Type: UserInputTypeImage, URL: url}
}

// LocalImageInput reads the file at path synchronously and produces an image input carrying a
// data: URI, so the payload is self-contained. The media type is inferred from the file
// extension, falling back to content sniffing.
//
// Upstream also has a `localImage` variant that takes the path instead; sending the bytes
// avoids depending on the server having access to the same filesystem.
func LocalImageInput(path string) (UserInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return UserInput{}, fmt.Errorf("local image input: %w", err)
	}
	mediaType := mime.TypeByExtension(filepath.Ext(path))
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	return UserInput{Type: UserInputTypeImage, URL: base64DataURI(mediaType, data)}, nil
}

// SkillInput references a named skill: {"type":"skill","name":...,"path":...}.
//
// path is required upstream. The former single-argument form sent only `name`, so the server
// rejected every skill input.
func SkillInput(name, path string) UserInput {
	return UserInput{Type: UserInputTypeSkill, Name: name, Path: path}
}

// MentionInput references an @mention resource: {"type":"mention","name":...,"path":...}.
//
// name and path are both required upstream. The former form sent `id` and `text` instead --
// neither is a field of the mention variant -- so it was rejected on every count.
func MentionInput(name, path string) UserInput {
	return UserInput{Type: UserInputTypeMention, Name: name, Path: path}
}

func base64DataURI(mediaType string, data []byte) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// FileChangePayload is the decoded payload for an ItemKindFileChange item.
type FileChangePayload struct {
	FilePath   string `json:"filePath"`
	ChangeType string `json:"changeType"` // "create", "modify", "delete"
	Diff       string `json:"diff,omitempty"`
	NewContent string `json:"newContent,omitempty"`
	OldContent string `json:"oldContent,omitempty"`
}

// CommandExecutionPayload is the decoded payload for an ItemKindCommandExecution item.
type CommandExecutionPayload struct {
	Command  string `json:"command"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

// ItemAsFileChange attempts to decode the item payload as a FileChangePayload.
// Returns nil if the item is not a fileChange or decoding fails.
func ItemAsFileChange(item Item) *FileChangePayload {
	if item.Kind != ItemKindFileChange {
		return nil
	}
	payload := item.PayloadBytes()
	if len(payload) == 0 {
		return nil
	}
	var p FileChangePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}
	return &p
}

// ItemAsCommandExecution attempts to decode the item payload as a CommandExecutionPayload.
// Returns nil if the item is not a commandExecution or decoding fails.
func ItemAsCommandExecution(item Item) *CommandExecutionPayload {
	if item.Kind != ItemKindCommandExecution {
		return nil
	}
	payload := item.PayloadBytes()
	if len(payload) == 0 {
		return nil
	}
	var p CommandExecutionPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}
	return &p
}
