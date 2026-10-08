package codexgo

import (
	"fmt"
	"sync"
	"time"
)

// Event subscriptions (plan T2.6).
//
// Design goals, in priority order:
//
//  1. publish NEVER waits for a subscriber. Waiting on a shared publish path would stall
//     every subscriber behind the slowest one (O(slow subscribers x timeout)), and adding a
//     forwarding goroutine or dropping a lock does not fix that -- the wait has to happen on
//     the subscriber's own path, which is what the forwarding goroutine below is for.
//  2. Termination is observable even if the consumer never reads C(). Err()/Done() are the
//     authoritative, out-of-band channel.
//  3. Loss is never silent: it is reported in-band (EventsLostEvent, written into a reserved
//     slot so it can never block) and out-of-band (EventsLostError).
//
// The old broker closed a subscriber's channel the moment its buffer filled, which reported
// loss only as an unexplained channel close and left no way to tell a slow consumer from a
// finished stream.

// EventsLostReason explains why a subscription stopped.
type EventsLostReason string

const (
	// EventsLostReasonClosed: Close() was called, or the client shut down.
	EventsLostReasonClosed EventsLostReason = "closed"

	// EventsLostReasonStall: the consumer stopped reading for longer than the configured
	// stall timeout. The subscription is terminated rather than blocking the publisher.
	EventsLostReasonStall EventsLostReason = "stall"

	// EventsLostReasonOverflow: the subscriber's backlog hit its bound, meaning events were
	// produced faster than they could be handed over for a sustained period.
	EventsLostReasonOverflow EventsLostReason = "overflow"
)

// EventsLostEvent is the last value delivered on a subscription's channel before it closes.
//
// A consumer that only ranges over C() can still see why the stream ended. The
// authoritative signal is Err(); this is the in-band convenience copy.
type EventsLostEvent struct {
	Reason    EventsLostReason
	LostCount int64
	// GapFrom and GapTo bound the window the lost events were offered in. Zero when
	// nothing was actually dropped.
	//
	// These are *time*, not sequence numbers: upstream notifications carry no sequence, so
	// there is no way to name a gap by position. A time window is what is derivable, and it
	// is enough to correlate the loss against what else was happening.
	GapFrom time.Time
	GapTo   time.Time
}

// EventsLostError is returned by EventSubscription.Err once the subscription has stopped,
// and is nil while it is healthy.
type EventsLostError struct {
	Reason    EventsLostReason
	LostCount int64
	// GapFrom and GapTo bound the window the lost events were offered in; see EventsLostEvent.
	GapFrom time.Time
	GapTo   time.Time
}

func (e *EventsLostError) Error() string {
	if e.GapFrom.IsZero() {
		return fmt.Sprintf("event subscription stopped: reason=%s lost=%d", e.Reason, e.LostCount)
	}
	return fmt.Sprintf("event subscription stopped: reason=%s lost=%d window=%s..%s",
		e.Reason, e.LostCount,
		e.GapFrom.Format(time.RFC3339Nano), e.GapTo.Format(time.RFC3339Nano))
}

const (
	// defaultEventOutCap is the per-subscriber delivery buffer. Must be >= 2: one slot is
	// permanently reserved for the terminal event.
	defaultEventOutCap = 128
	// defaultEventMaxBacklog bounds how far a subscriber may fall behind before its
	// subscription is terminated as overflowed.
	defaultEventMaxBacklog = 4096
	// defaultEventStallTimeout bounds how long a non-reading consumer may hold a full
	// delivery buffer before its subscription is terminated.
	defaultEventStallTimeout = 5 * time.Second
	// defaultEventPollInterval is how often the forwarding goroutine re-checks for consumer
	// progress while the buffer is full.
	defaultEventPollInterval = 50 * time.Millisecond
)

type eventBrokerConfig struct {
	outCap       int
	maxBacklog   int
	stallTimeout time.Duration
	pollInterval time.Duration
}

func defaultEventBrokerConfig() eventBrokerConfig {
	return eventBrokerConfig{
		outCap:       defaultEventOutCap,
		maxBacklog:   defaultEventMaxBacklog,
		stallTimeout: defaultEventStallTimeout,
		pollInterval: defaultEventPollInterval,
	}
}

// queuedEvent is a pending event plus the moment it was offered, so a loss can be reported
// as a time window rather than just a count.
type queuedEvent struct {
	event Event
	at    time.Time
}

// eventSubscriber owns one subscription's buffers and its forwarding goroutine.
type eventSubscriber struct {
	broker *eventBroker
	id     uint64
	cfg    eventBrokerConfig

	out    chan Event
	notify chan struct{} // cap 1, merged wakeup: "there may be more backlog"
	stop   chan struct{} // closed once by whichever party terminates first
	done   chan struct{} // closed by the forwarding goroutine as it exits

	stopOnce sync.Once
	doneOnce sync.Once

	mu        sync.Mutex // guards backlog / reason / terminal / lostCount
	backlog   []queuedEvent
	reason    EventsLostReason
	terminal  bool
	lostCount int64
}

// offer appends without ever waiting. Called from the publish path, one subscriber at a
// time, which is why it must not block.
func (s *eventSubscriber) offer(ev Event) {
	s.mu.Lock()
	if s.terminal {
		s.mu.Unlock()
		return
	}
	if len(s.backlog) >= s.cfg.maxBacklog {
		s.lostCount++
		s.setTerminalLocked(EventsLostReasonOverflow)
		s.mu.Unlock()
		return
	}
	s.backlog = append(s.backlog, queuedEvent{event: ev, at: time.Now()})
	s.mu.Unlock()
	s.wake()
}

// wake signals the forwarding goroutine. A non-blocking send is correct: notify is a merged
// signal, and a dropped signal is harmless because the goroutine re-checks the backlog.
func (s *eventSubscriber) wake() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// setTerminalLocked marks the subscription stopped. The caller must hold s.mu. The first
// reason wins, so a stall stays a stall even if Close() races it.
func (s *eventSubscriber) setTerminalLocked(reason EventsLostReason) {
	if s.terminal {
		return
	}
	s.terminal = true
	s.reason = reason
	s.stopOnce.Do(func() { close(s.stop) })
}

func (s *eventSubscriber) setTerminal(reason EventsLostReason) {
	s.mu.Lock()
	s.setTerminalLocked(reason)
	s.mu.Unlock()
}

// terminalState reports whether the subscription stopped, and how much was lost.
func (s *eventSubscriber) terminalState() (EventsLostReason, int64, time.Time, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.terminal {
		return "", 0, time.Time{}, time.Time{}, false
	}
	// Everything still queued is lost too, not just what overflow already counted.
	lost := s.lostCount + int64(len(s.backlog))
	// The undelivered entries span a real window: from when the oldest was offered to when
	// the newest was. A pure overflow with an empty backlog has no window to report.
	var from, to time.Time
	if len(s.backlog) > 0 {
		from, to = s.backlog[0].at, s.backlog[len(s.backlog)-1].at
	}
	return s.reason, lost, from, to, true
}

func (s *eventSubscriber) stopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminal
}

// forward is the only writer of s.out and the only closer of s.out/s.done. All blocking
// waits live here, so a slow consumer can only ever stall itself.
func (s *eventSubscriber) forward() {
	defer func() {
		// In-band termination. A plain send would be a hazard if the reserve ever broke, so
		// it is a non-blocking send: the out-of-band Err()/Done() pair is authoritative.
		if reason, lost, from, to, ok := s.terminalState(); ok {
			select {
			case s.out <- Event{Value: EventsLostEvent{
				Reason: reason, LostCount: lost, GapFrom: from, GapTo: to,
			}}:
			default:
			}
		}
		close(s.out)
		s.doneOnce.Do(func() { close(s.done) })
		s.broker.unsubscribe(s.id)
	}()

	var stallDeadline time.Time

loop:
	for {
		if s.stopped() {
			break
		}

		s.mu.Lock()
		if len(s.backlog) == 0 {
			s.mu.Unlock()
			// Both waits must be interruptible by stop. Waiting on notify alone would hang
			// forever for a subscription that is closed before any event arrives, because
			// notify is a merged signal that may already hold an unread token.
			select {
			case <-s.notify:
			case <-s.stop:
			}
			continue
		}
		head := s.backlog[0].event

		// Slot reservation: ordinary events may occupy at most outCap-1 slots, leaving the
		// last one for the terminal event. This goroutine is the only writer of s.out and
		// readers only ever reduce len(), so the reservation holds.
		if len(s.out) >= s.cfg.outCap-1 {
			s.mu.Unlock()
			if stallDeadline.IsZero() {
				stallDeadline = time.Now()
			}
			if time.Since(stallDeadline) > s.cfg.stallTimeout {
				s.setTerminal(EventsLostReasonStall)
				break loop
			}
			select {
			case <-time.After(s.cfg.pollInterval):
			case <-s.stop:
				break loop
			}
			continue
		}
		s.mu.Unlock()

		// No stall timer here: the guard above proved there is room, so this send cannot
		// block. Only stop needs to interrupt it.
		select {
		case s.out <- head:
			s.mu.Lock()
			if len(s.backlog) > 0 {
				s.backlog = s.backlog[1:]
			}
			s.mu.Unlock()
			stallDeadline = time.Time{}
		case <-s.stop:
			break loop
		}
	}
}

func newClosedEventSubscriber() *eventSubscriber {
	s := &eventSubscriber{
		cfg:      defaultEventBrokerConfig(),
		out:      make(chan Event, 1),
		notify:   make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		terminal: true,
		reason:   EventsLostReasonClosed,
	}
	close(s.out)
	close(s.done)
	return s
}

// EventSubscription is a live stream of events.
//
// Termination is observable three ways, so a consumer cannot miss it: C() closes, Done()
// closes, and Err() becomes non-nil. Close is idempotent, safe to call concurrently, and
// does not wait.
type EventSubscription struct {
	sub *eventSubscriber
}

// C returns the delivery channel. It closes once the subscription stops, after the terminal
// EventsLostEvent has been delivered.
func (s *EventSubscription) C() <-chan Event {
	if s == nil || s.sub == nil {
		return nil
	}
	return s.sub.out
}

// Done returns a channel closed when the subscription stops. Unlike C() it does not depend
// on the consumer draining the buffer, so it is the reliable way to observe termination.
func (s *EventSubscription) Done() <-chan struct{} {
	if s == nil || s.sub == nil {
		return nil
	}
	return s.sub.done
}

// Err returns nil while the subscription is healthy, and an *EventsLostError describing the
// reason and loss count once it has stopped. It never blocks and does not require reading C().
func (s *EventSubscription) Err() error {
	if s == nil || s.sub == nil {
		return nil
	}
	reason, lost, from, to, ok := s.sub.terminalState()
	if !ok {
		return nil
	}
	return &EventsLostError{Reason: reason, LostCount: lost, GapFrom: from, GapTo: to}
}

// Close stops the subscription and returns immediately. It is idempotent and does not wait
// for the forwarding goroutine, which exits on its own and then closes C() and Done().
func (s *EventSubscription) Close() {
	if s == nil || s.sub == nil {
		return
	}
	s.sub.setTerminal(EventsLostReasonClosed)
}

// eventBroker fans events out to subscribers. The zero value is not usable; use
// newEventBroker.
type eventBroker struct {
	cfg eventBrokerConfig

	mu     sync.Mutex
	nextID uint64
	subs   map[uint64]*eventSubscriber
	closed bool
}

func newEventBroker(cfg eventBrokerConfig) *eventBroker {
	if cfg.outCap < 2 {
		// A one-slot buffer would leave no room for the reserved terminal slot.
		cfg.outCap = defaultEventOutCap
	}
	if cfg.maxBacklog <= 0 {
		cfg.maxBacklog = defaultEventMaxBacklog
	}
	if cfg.stallTimeout <= 0 {
		cfg.stallTimeout = defaultEventStallTimeout
	}
	if cfg.pollInterval <= 0 {
		cfg.pollInterval = defaultEventPollInterval
	}
	return &eventBroker{cfg: cfg, subs: make(map[uint64]*eventSubscriber)}
}

// Subscribe registers a new subscriber. On an already-closed broker it returns a
// subscription that is closed from the start, so callers never block on a dead stream.
func (b *eventBroker) Subscribe() *EventSubscription {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return &EventSubscription{sub: newClosedEventSubscriber()}
	}
	b.nextID++
	id := b.nextID
	s := &eventSubscriber{
		broker: b,
		id:     id,
		cfg:    b.cfg,
		out:    make(chan Event, b.cfg.outCap),
		notify: make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	b.subs[id] = s
	b.mu.Unlock()

	go s.forward()
	return &EventSubscription{sub: s}
}

// publish hands ev to every subscriber without waiting for any of them. Cost is
// O(#subscribers) appends and atomic-free signal attempts; no timeout is ever awaited here.
func (b *eventBroker) publish(ev Event) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	subs := make([]*eventSubscriber, 0, len(b.subs))
	for _, s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()

	for _, s := range subs {
		s.offer(ev)
	}
}

// close terminates every subscription. Each subscriber's forwarding goroutine still performs
// its own close(out)/close(done), so no channel is closed twice.
func (b *eventBroker) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := make([]*eventSubscriber, 0, len(b.subs))
	for _, s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()

	for _, s := range subs {
		s.setTerminal(EventsLostReasonClosed)
	}
}

// unsubscribe drops a finished subscriber. Called by the forwarding goroutine on exit, never
// while holding a subscriber lock, so broker -> subscriber lock ordering is preserved.
func (b *eventBroker) unsubscribe(id uint64) {
	b.mu.Lock()
	delete(b.subs, id)
	b.mu.Unlock()
}
