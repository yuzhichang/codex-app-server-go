package transport

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"time"
)

// RetryConfig controls the exponential back-off retry behaviour.
type RetryConfig struct {
	MaxAttempts int           // default 5
	BaseDelay   time.Duration // default 200ms
	MaxDelay    time.Duration // default 30s
	Multiplier  float64       // default 2.0
	JitterFrac  float64       // jitter as fraction of computed delay, default 0.25
}

// DefaultRetryConfig returns a RetryConfig with sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 5,
		BaseDelay:   200 * time.Millisecond,
		MaxDelay:    30 * time.Second,
		Multiplier:  2.0,
		JitterFrac:  0.25,
	}
}

func (c RetryConfig) withDefaults() RetryConfig {
	d := DefaultRetryConfig()
	if c.MaxAttempts > 0 {
		d.MaxAttempts = c.MaxAttempts
	}
	if c.BaseDelay > 0 {
		d.BaseDelay = c.BaseDelay
	}
	if c.MaxDelay > 0 {
		d.MaxDelay = c.MaxDelay
	}
	if c.Multiplier > 0 {
		d.Multiplier = c.Multiplier
	}
	if c.JitterFrac > 0 {
		d.JitterFrac = c.JitterFrac
	}
	return d
}

// RetryTransport wraps an inner Transport and retries failed Call() attempts
// using exponential back-off with jitter. Only errors that satisfy
// isRetryable() are retried; others are returned immediately.
//
// Notify() and server-request routing are passed through without retry.
type RetryTransport struct {
	inner  Transport
	config RetryConfig
}

// NewRetryTransport wraps inner with retry logic using the given config.
func NewRetryTransport(inner Transport, cfg RetryConfig) *RetryTransport {
	return &RetryTransport{inner: inner, config: cfg.withDefaults()}
}

// Call implements Transport. Retries on retryable errors up to MaxAttempts.
func (r *RetryTransport) Call(ctx context.Context, method string, params any, result any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var err error
	for attempt := 0; attempt < r.config.MaxAttempts; attempt++ {
		err = r.inner.Call(ctx, method, params, result)
		if err == nil || !isRetryable(method, err) {
			return err
		}
		if attempt == r.config.MaxAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.backoff(attempt)):
		}
	}
	return err
}

func (r *RetryTransport) backoff(attempt int) time.Duration {
	delay := float64(r.config.BaseDelay) * math.Pow(r.config.Multiplier, float64(attempt))
	if delay > float64(r.config.MaxDelay) {
		delay = float64(r.config.MaxDelay)
	}
	jitter := r.config.JitterFrac*rand.Float64()*2 - r.config.JitterFrac
	delay *= 1 + jitter
	if delay < 0 {
		delay = 0
	}
	return time.Duration(delay)
}

// Notify delegates to the inner transport without retry.
func (r *RetryTransport) Notify(ctx context.Context, method string, params any) error {
	return r.inner.Notify(ctx, method, params)
}

// Requests delegates to the inner transport.
func (r *RetryTransport) Requests() <-chan *Request { return r.inner.Requests() }

// Notifications delegates to the inner transport.
func (r *RetryTransport) Notifications() <-chan Notification { return r.inner.Notifications() }

// Done delegates to the inner transport.
func (r *RetryTransport) Done() <-chan struct{} { return r.inner.Done() }

// Close delegates to the inner transport.
func (r *RetryTransport) Close() error { return r.inner.Close() }

// readOnlyMethods are the stable client requests that are safe to re-issue after the
// connection drops.
//
// The distinction matters only for ErrClosed. Errors the *server* reports (429,
// HttpConnectionFailed, internal -32603) are responses -- the request was answered, so
// re-issuing cannot double-apply and every method may be retried. ErrClosed is the local
// transport saying the connection went away, and the request may already have been applied
// server-side; replaying it is only safe when it has no side effect.
//
// Deliberately an explicit list rather than a name heuristic: wrongly retrying a mutation
// (a plugin install, a config write, a turn start) is much worse than missing a retry.
// Derived from gen/method-surface.json by name and reviewed by hand.
var readOnlyMethods = map[string]struct{}{
	"account/gatewayOAuth/read":       {},
	"account/rateLimits/read":         {},
	"account/read":                    {},
	"account/usage/read":              {},
	"account/workspaceMessages/read":  {},
	"app/installed":                   {},
	"app/list":                        {},
	"app/read":                        {},
	"config/read":                     {},
	"configRequirements/read":         {},
	"experimentalFeature/list":        {},
	"externalAgentConfig/detect":      {},
	"fs/getMetadata":                  {},
	"fs/readDirectory":                {},
	"fs/readFile":                     {},
	"fuzzyFileSearch":                 {},
	"hooks/list":                      {},
	"mcpServer/resource/read":         {},
	"mcpServerStatus/list":            {},
	"model/list":                      {},
	"modelProvider/capabilities/read": {},
	"permissionProfile/list":          {},
	"plugin/installed":                {},
	"plugin/list":                     {},
	"plugin/read":                     {},
	"plugin/share/list":               {},
	"plugin/skill/read":               {},
	"skills/list":                     {},
	"thread/attachment/list":          {},
	"thread/attachmentOwner/list":     {},
	"thread/items/list":               {},
	"thread/list":                     {},
	"thread/loaded/list":              {},
	"thread/read":                     {},
	"thread/turns/list":               {},
	"threadSection/list":              {},
	"windowsSandbox/readiness":        {},
}

// IsReadOnlyMethod reports whether re-issuing method after an ambiguous connection failure
// is safe. Exported for callers that need the same judgement (e.g. a session supervisor
// deciding what to replay after a reconnect).
func IsReadOnlyMethod(method string) bool {
	_, ok := readOnlyMethods[method]
	return ok
}

// isRetryable reports whether a Call error is worth retrying, given the method being called.
//
// method is part of the decision because retrying is only unconditionally safe when the
// request was definitely answered, or when re-issuing cannot have a side effect. See
// readOnlyMethods.
func isRetryable(method string, err error) bool {
	if err == nil {
		return false
	}
	// Server-reported outcomes: the RPC completed with an error, so retrying is safe for
	// any method. These are not "the connection died mid-flight".
	if isRateLimited(err) || isHTTPConnectionFailed(err) {
		return true
	}
	if errors.Is(err, ErrClosed) {
		// The connection went away and we do not know whether the call was applied.
		// Previously this returned false outright (retry.go), which turned every in-flight
		// call into a hard failure the moment a connection dropped.
		return IsReadOnlyMethod(method)
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == -32603 {
		return true
	}
	return false
}

// codexErrorInfo mirrors the structured payload carried in RPCError.Data so the
// transport package can classify errors without importing the parent package
// (which would create a circular dependency).
func codexErrorInfo(err error) (errType string, httpStatus int, ok bool) {
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || len(rpcErr.Data) == 0 {
		return "", 0, false
	}
	var envelope struct {
		CodexErrorInfo struct {
			Type           string `json:"type"`
			HTTPStatusCode int    `json:"httpStatusCode"`
		} `json:"codexErrorInfo"`
	}
	if json.Unmarshal(rpcErr.Data, &envelope) != nil {
		return "", 0, false
	}
	return envelope.CodexErrorInfo.Type, envelope.CodexErrorInfo.HTTPStatusCode, true
}

func isRateLimited(err error) bool {
	_, status, ok := codexErrorInfo(err)
	return ok && status == 429
}

func isHTTPConnectionFailed(err error) bool {
	errType, _, ok := codexErrorInfo(err)
	return ok && errType == "HttpConnectionFailed"
}

// Ensure RetryTransport satisfies Transport at compile time.
var _ Transport = (*RetryTransport)(nil)
