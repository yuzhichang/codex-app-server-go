package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// Sentinel errors returned by transport operations.
var (
	ErrClosed               = errors.New("transport closed")
	ErrDisconnected         = errors.New("transport disconnected")
	ErrReconnectUnsupported = errors.New("transport reconnect unsupported")
	ErrAlreadyReplied       = errors.New("request already replied")
	ErrProtocol             = errors.New("invalid JSON-RPC message")
)

// Opener opens a new JSON-RPC stream for reconnect.
type Opener func(context.Context) (io.ReadWriteCloser, error)

// RPCError is the JSON-RPC error envelope.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if len(e.Data) == 0 {
		return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("rpc error %d: %s: %s", e.Code, e.Message, string(e.Data))
}

// Notification represents a server notification delivered from the transport.
type Notification struct {
	Method string
	Params json.RawMessage
	// EmittedAtMs is the envelope-level timestamp upstream attaches to a notification.
	//
	// It is a SIBLING of `method`/`params` on the wire --
	// {"method":...,"params":{...},"emittedAtMs":123} -- because upstream flattens the
	// notification and adds the field alongside it. It is therefore NOT part of Params, and
	// was silently dropped by every reader that only looked at raw["params"]. Zero means the
	// server did not send one.
	//
	// Covered by the WebSocket and HTTP read loops. NOT covered on stdio, which is backed by
	// jrpc2: it parses the envelope itself and exposes only method and params, so a sibling
	// field never reaches the SDK there. Use WebSocket if the timestamp matters.
	EmittedAtMs int64
}

// envelopeEmittedAtMs extracts the envelope-level timestamp from a raw notification.
//
// Centralised so the three read loops (websocket, http, jsonrpc) cannot drift: a field that
// is easy to forget once is easy to forget in two of three places.
func envelopeEmittedAtMs(raw map[string]json.RawMessage) int64 {
	value, ok := raw["emittedAtMs"]
	if !ok {
		return 0
	}
	var ms int64
	if err := json.Unmarshal(value, &ms); err != nil {
		return 0
	}
	return ms
}

// DecodeParams decodes the notification params into v.
func (n Notification) DecodeParams(v any) error {
	if len(n.Params) == 0 {
		return json.Unmarshal([]byte("null"), v)
	}
	return json.Unmarshal(n.Params, v)
}

// replyResult holds the outcome of a Request reply for transports that need
// to bridge back asynchronously (e.g. StdioTransport wrapping jrpc2).
type replyResult struct {
	result any
	code   int
	msg    string
	data   any
	isErr  bool
}

// Request represents a server-initiated request that may be replied to later.
type Request struct {
	// Used by JSONRPCTransport:
	transport *JSONRPCTransport
	sessionID uint64
	id        json.RawMessage

	// Used by StdioTransport (jrpc2-backed): replyCh receives exactly one value.
	replyCh chan replyResult

	method string
	params json.RawMessage
	ctx    context.Context

	mu      sync.Mutex
	replied bool
}

// ID returns the raw JSON-RPC request id.
func (r *Request) ID() json.RawMessage { return cloneRaw(r.id) }

// Method returns the request method.
func (r *Request) Method() string { return r.method }

// Params returns the raw params payload.
func (r *Request) Params() json.RawMessage { return cloneRaw(r.params) }

// Context returns a context that is canceled when the session ends.
func (r *Request) Context() context.Context { return r.ctx }

// DecodeParams decodes the request params into v.
func (r *Request) DecodeParams(v any) error {
	if len(r.params) == 0 {
		return json.Unmarshal([]byte("null"), v)
	}
	return json.Unmarshal(r.params, v)
}

// Reply sends a success response for the request.
func (r *Request) Reply(ctx context.Context, result any) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.replied {
		return ErrAlreadyReplied
	}
	r.replied = true

	if r.replyCh != nil {
		// jrpc2-backed path: replyCh has cap 1 and the replied guard above
		// prevents double-sends, so this send never blocks and never panics.
		r.replyCh <- replyResult{result: result}
		return nil
	}

	if r.transport == nil {
		return ErrClosed
	}
	return r.transport.send(ctx, r.sessionID, replyEnvelope{
		ID:     cloneRaw(r.id),
		Result: result,
	})
}

// ReplyError sends a JSON-RPC error response for the request.
func (r *Request) ReplyError(ctx context.Context, code int, message string, data any) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.replied {
		return ErrAlreadyReplied
	}
	r.replied = true

	if r.replyCh != nil {
		// jrpc2-backed path: replyCh has cap 1 and the replied guard above
		// prevents double-sends, so this send never blocks and never panics.
		r.replyCh <- replyResult{isErr: true, code: code, msg: message, data: data}
		return nil
	}

	if r.transport == nil {
		return ErrClosed
	}
	rpcErr := &RPCError{Code: code, Message: message}
	if data != nil {
		raw, marshalErr := json.Marshal(data)
		if marshalErr != nil {
			return marshalErr
		}
		rpcErr.Data = raw
	}
	return r.transport.send(ctx, r.sessionID, replyEnvelope{
		ID:    cloneRaw(r.id),
		Error: rpcErr,
	})
}

// Transport is the lower-level internal interface that all transport
// implementations must satisfy.
type Transport interface {
	// Call sends a JSON-RPC request and decodes the result.
	Call(ctx context.Context, method string, params any, result any) error
	// Notify sends a JSON-RPC notification (no reply expected).
	Notify(ctx context.Context, method string, params any) error
	// Requests returns a channel of server-initiated requests.
	Requests() <-chan *Request
	// Notifications returns a channel of server notifications.
	Notifications() <-chan Notification
	// Done is closed when the transport is permanently terminated.
	Done() <-chan struct{}
	// Close shuts down the transport.
	Close() error
}

// --- JSONRPCTransport (custom newline-delimited JSON-RPC implementation) ---

type callResult struct {
	raw    json.RawMessage
	err    error
	rpcErr *RPCError
}

// JSONRPCTransport is a low-level, custom JSON-RPC transport that reads and
// writes newline-delimited JSON over an io.ReadWriteCloser. It implements
// Transport and supports optional reconnection via an Opener.
type JSONRPCTransport struct {
	mu         sync.Mutex
	conn       io.ReadWriteCloser
	opener     Opener
	closed     bool
	sessionID  uint64
	nextID     uint64
	pending    map[uint64]chan callResult
	requests   chan *Request
	notes      chan Notification
	done       chan struct{}
	doneOnce   sync.Once
	writeMu    sync.Mutex
	sessionCtx context.Context
	cancel     context.CancelFunc
}

// New creates a transport bound to an existing JSON-RPC stream.
func New(conn io.ReadWriteCloser) *JSONRPCTransport {
	return newTransport(conn, nil)
}

func newTransport(conn io.ReadWriteCloser, opener Opener) *JSONRPCTransport {
	ctx, cancel := context.WithCancel(context.Background())
	t := &JSONRPCTransport{
		conn:       conn,
		opener:     opener,
		pending:    make(map[uint64]chan callResult),
		requests:   make(chan *Request, 32),
		notes:      make(chan Notification, 32),
		done:       make(chan struct{}),
		sessionCtx: ctx,
		cancel:     cancel,
	}
	t.startSessionLocked()
	return t
}

// Done is closed when the transport is permanently closed.
func (t *JSONRPCTransport) Done() <-chan struct{} { return t.done }

// Requests returns inbound server requests.
func (t *JSONRPCTransport) Requests() <-chan *Request { return t.requests }

// Notifications returns inbound server notifications.
func (t *JSONRPCTransport) Notifications() <-chan Notification { return t.notes }

// Call sends a request and decodes the result into result.
func (t *JSONRPCTransport) Call(ctx context.Context, method string, params any, result any) error {
	payload, err := buildCall(method, params)
	if err != nil {
		return err
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrClosed
	}
	if t.conn == nil {
		t.mu.Unlock()
		return ErrDisconnected
	}
	t.nextID++
	id := t.nextID
	replyCh := make(chan callResult, 1)
	t.pending[id] = replyCh
	conn := t.conn
	t.mu.Unlock()

	env := payload.withID(id)
	env.Trace = TraceContextFromContext(ctx)
	if err := t.writeJSON(conn, env); err != nil {
		t.removePending(id)
		return err
	}

	select {
	case <-ctx.Done():
		t.removePending(id)
		return ctx.Err()
	case res := <-replyCh:
		if res.err != nil {
			return res.err
		}
		if res.rpcErr != nil {
			return res.rpcErr
		}
		if result == nil || len(res.raw) == 0 {
			return nil
		}
		return json.Unmarshal(res.raw, result)
	case <-t.done:
		t.removePending(id)
		return ErrClosed
	}
}

// Notify sends a JSON-RPC notification.
func (t *JSONRPCTransport) Notify(ctx context.Context, method string, params any) error {
	payload := notificationEnvelope{Method: method, Params: params}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrClosed
	}
	conn := t.conn
	if conn == nil {
		t.mu.Unlock()
		return ErrDisconnected
	}
	t.mu.Unlock()

	return t.writeJSON(conn, payload)
}

// Close shuts down the active session and permanently closes the transport.
func (t *JSONRPCTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	conn := t.conn
	t.conn = nil
	pending := t.flushPendingLocked()
	cancel := t.cancel
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
	for _, ch := range pending {
		select {
		case ch <- callResult{err: ErrClosed}:
		default:
		}
	}
	t.doneOnce.Do(func() { close(t.done) })
	return nil
}

func (t *JSONRPCTransport) startSessionLocked() {
	t.mu.Lock()
	if t.conn == nil || t.closed {
		t.mu.Unlock()
		return
	}
	t.sessionID++
	sid := t.sessionID
	conn := t.conn
	ctx, cancel := context.WithCancel(context.Background())
	oldCancel := t.cancel
	t.sessionCtx = ctx
	t.cancel = cancel
	t.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	go t.readLoop(conn, sid, ctx)
}

func (t *JSONRPCTransport) readLoop(conn io.ReadWriteCloser, sid uint64, ctx context.Context) {
	dec := json.NewDecoder(conn)
	for {
		var raw map[string]json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			t.handleSessionEnd(sid, err)
			return
		}
		if !t.isCurrentSession(sid) {
			return
		}
		if methodRaw, ok := raw["method"]; ok {
			var method string
			if err := json.Unmarshal(methodRaw, &method); err != nil {
				t.handleSessionEnd(sid, err)
				return
			}
			params := cloneRaw(raw["params"])
			if idRaw, ok := raw["id"]; ok {
				req := &Request{
					transport: t,
					sessionID: sid,
					id:        cloneRaw(idRaw),
					method:    method,
					params:    params,
					ctx:       ctx,
				}
				if !t.deliverRequest(sid, req) {
					return
				}
			} else {
				if !t.deliverNotification(sid, Notification{Method: method, Params: params, EmittedAtMs: envelopeEmittedAtMs(raw)}) {
					return
				}
			}
			continue
		}

		idRaw, ok := raw["id"]
		if !ok {
			t.handleSessionEnd(sid, ErrProtocol)
			return
		}
		id, err := parseID(idRaw)
		if err != nil {
			t.handleSessionEnd(sid, err)
			return
		}
		res := callResult{raw: cloneRaw(raw["result"])}
		if errRaw, ok := raw["error"]; ok {
			var rpcErr RPCError
			if err := json.Unmarshal(errRaw, &rpcErr); err != nil {
				t.handleSessionEnd(sid, err)
				return
			}
			res.rpcErr = &rpcErr
		}
		if !t.deliverResponse(sid, id, res) {
			return
		}
	}
}

func (t *JSONRPCTransport) isCurrentSession(sid uint64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closed && t.sessionID == sid
}

func (t *JSONRPCTransport) deliverRequest(sid uint64, req *Request) bool {
	t.mu.Lock()
	alive := !t.closed && t.sessionID == sid
	t.mu.Unlock()
	if !alive {
		return false
	}
	select {
	case t.requests <- req:
		return true
	case <-t.done:
		return false
	}
}

func (t *JSONRPCTransport) deliverNotification(sid uint64, n Notification) bool {
	t.mu.Lock()
	alive := !t.closed && t.sessionID == sid
	t.mu.Unlock()
	if !alive {
		return false
	}
	select {
	case t.notes <- n:
		return true
	case <-t.done:
		return false
	}
}

func (t *JSONRPCTransport) deliverResponse(sid uint64, id uint64, res callResult) bool {
	t.mu.Lock()
	if t.closed || t.sessionID != sid {
		t.mu.Unlock()
		return false
	}
	ch, ok := t.pending[id]
	if ok {
		delete(t.pending, id)
	}
	t.mu.Unlock()
	if !ok {
		return true
	}
	select {
	case ch <- res:
		return true
	case <-t.done:
		return false
	}
}

func (t *JSONRPCTransport) handleSessionEnd(sid uint64, err error) {
	t.mu.Lock()
	if t.closed || t.sessionID != sid {
		t.mu.Unlock()
		return
	}
	t.conn = nil
	pending := t.flushPendingLocked()
	cancel := t.cancel
	t.cancel = nil
	t.sessionCtx = context.Background()
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, ch := range pending {
		select {
		case ch <- callResult{err: ErrDisconnected}:
		default:
		}
	}
}

func (t *JSONRPCTransport) flushPendingLocked() map[uint64]chan callResult {
	pending := t.pending
	t.pending = make(map[uint64]chan callResult)
	return pending
}

func (t *JSONRPCTransport) removePending(id uint64) {
	t.mu.Lock()
	delete(t.pending, id)
	t.mu.Unlock()
}

func (t *JSONRPCTransport) send(ctx context.Context, sessionID uint64, payload any) error {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	t.mu.Lock()
	if t.closed || t.sessionID != sessionID {
		t.mu.Unlock()
		if t.closed {
			return ErrClosed
		}
		return ErrDisconnected
	}
	conn := t.conn
	t.mu.Unlock()
	if conn == nil {
		return ErrDisconnected
	}
	return t.writeJSON(conn, payload)
}

func (t *JSONRPCTransport) writeJSON(conn io.ReadWriteCloser, payload any) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	enc := json.NewEncoder(conn)
	enc.SetEscapeHTML(false)
	return enc.Encode(payload)
}

// --- envelope types ---

type callEnvelope struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

func (c callEnvelope) withID(id uint64) requestEnvelope {
	return requestEnvelope{Method: c.Method, ID: id, Params: c.Params}
}

// TraceContext is the W3C Trace Context carried on JSON-RPC requests.
//
// Upstream defines this in a different crate (`codex-rs/protocol/src/protocol.rs`), so it
// does NOT appear in the generated app-server schema; the shape is mirrored here.
type TraceContext struct {
	Traceparent string `json:"traceparent,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
}

// traceContextKey carries an optional *TraceContext through a context.Context.
type traceContextKey struct{}

// WithTraceContext returns a context carrying tc, to be attached to outbound requests.
func WithTraceContext(ctx context.Context, tc *TraceContext) context.Context {
	if tc == nil {
		return ctx
	}
	return context.WithValue(ctx, traceContextKey{}, tc)
}

// TraceContextFromContext returns the *TraceContext attached to ctx, if any.
func TraceContextFromContext(ctx context.Context) *TraceContext {
	if ctx == nil {
		return nil
	}
	tc, _ := ctx.Value(traceContextKey{}).(*TraceContext)
	return tc
}

// NOTE ON WIRE SHAPE (D1 / decision R3)
//
// Upstream is explicit (`app-server-protocol/src/rpc.rs:1-2`):
//
//	//! We do not do true JSON-RPC 2.0, as we neither send nor expect the
//	//! "jsonrpc": "2.0" field.
//
// The envelopes below therefore carry NO `jsonrpc` field. Do not reintroduce it, and do
// not "fix" its absence on the outbound path.
//
// One related deviation is unavoidable and intentional: the stdio transport is backed by
// jrpc2, a strict JSON-RPC 2.0 implementation that both emits and requires the field. Its
// inbound workaround lives in stdio.go (versionFixerReader) and must stay until/unless
// jrpc2 is replaced. The transports below are the ones we control, so they are aligned.
type requestEnvelope struct {
	Method string        `json:"method"`
	ID     uint64        `json:"id"`
	Params any           `json:"params,omitempty"`
	Trace  *TraceContext `json:"trace,omitempty"`
}

type notificationEnvelope struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type replyEnvelope struct {
	Method string          `json:"method,omitempty"`
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result"`
	Error  *RPCError       `json:"error,omitempty"`
}

// --- helpers ---

func buildCall(method string, params any) (callEnvelope, error) {
	if method == "" {
		return callEnvelope{}, errors.New("method is required")
	}
	return callEnvelope{Method: method, Params: params}, nil
}

func parseID(raw json.RawMessage) (uint64, error) {
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		v, err := strconv.ParseUint(num.String(), 10, 64)
		if err == nil {
			return v, nil
		}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		v, err := strconv.ParseUint(s, 10, 64)
		if err == nil {
			return v, nil
		}
	}
	return 0, ErrProtocol
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}
