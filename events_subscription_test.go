package codexgo

import (
	"errors"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Tests for the event-delivery contract in plan T2.6. These are internal tests because the
// contract is about buffer occupancy, reserved slots and termination reasons, none of which
// are observable through the public API alone.

func testBrokerConfig() eventBrokerConfig {
	return eventBrokerConfig{
		outCap:       4,
		maxBacklog:   4096,
		stallTimeout: 3 * time.Second,
		pollInterval: 2 * time.Millisecond,
	}
}

func lostError(t *testing.T, sub *EventSubscription) *EventsLostError {
	t.Helper()
	var e *EventsLostError
	if !errors.As(sub.Err(), &e) {
		t.Fatalf("Err() = %v, want *EventsLostError", sub.Err())
	}
	return e
}

// A9: a subscription that never received an event must still terminate on Close. The old
// design could block forever waiting on a merged notify signal that nobody would ever
// signal, leaving C() and Done() open and leaking the goroutine.
func TestIdleSubscriberCloseTerminates(t *testing.T) {
	b := newEventBroker(testBrokerConfig())
	defer b.close()

	sub := b.Subscribe()
	if err := sub.Err(); err != nil {
		t.Fatalf("fresh subscription reports Err() = %v", err)
	}

	sub.Close()

	select {
	case <-sub.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done() never closed for an idle subscription (the A9 hang)")
	}

	drained := make(chan struct{})
	go func() {
		for range sub.C() { //nolint:revive // draining
		}
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("C() never closed")
	}

	if got := lostError(t, sub); got.Reason != EventsLostReasonClosed {
		t.Fatalf("reason = %q, want %q", got.Reason, EventsLostReasonClosed)
	}

	// Close is idempotent and safe to call again.
	sub.Close()
	sub.Close()
}

// Close must not race a termination that already happened, and must not rewrite the reason:
// the first party to terminate decides why.
func TestCloseAfterStallPreservesReason(t *testing.T) {
	cfg := testBrokerConfig()
	cfg.outCap = 2
	cfg.stallTimeout = 40 * time.Millisecond
	cfg.pollInterval = time.Millisecond
	b := newEventBroker(cfg)
	defer b.close()

	sub := b.Subscribe()
	for i := 0; i < 16; i++ {
		b.publish(Event{Method: "e"})
	}

	select {
	case <-sub.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("a stalled subscription was never terminated")
	}
	if got := lostError(t, sub); got.Reason != EventsLostReasonStall {
		t.Fatalf("reason = %q, want %q", got.Reason, EventsLostReasonStall)
	}

	sub.Close()
	if got := lostError(t, sub); got.Reason != EventsLostReasonStall {
		t.Fatalf("Close() rewrote the reason to %q; first termination must win", got.Reason)
	}
}

// A8: ordinary events must never occupy the last slot, so the terminal event can always be
// written without blocking -- and publish must never wait while a consumer ignores it.
func TestSubscriberReservesTerminalSlot(t *testing.T) {
	cfg := testBrokerConfig()
	b := newEventBroker(cfg)
	defer b.close()

	sub := b.Subscribe()

	const sent = 200
	start := time.Now()
	for i := 0; i < sent; i++ {
		b.publish(Event{Method: "e"})
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("publish blocked for %v while nobody consumed", elapsed)
	}

	// Wait for the forwarding goroutine to saturate the buffer.
	deadline := time.Now().Add(2 * time.Second)
	for len(sub.C()) < cfg.outCap-1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := len(sub.C()); got > cfg.outCap-1 {
		t.Fatalf("buffer occupancy = %d, want <= %d (outCap-1); the reserved slot was used", got, cfg.outCap-1)
	}

	sub.Close()

	select {
	case <-sub.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done() never closed")
	}

	// The terminal event must be present, and last.
	var seen []Event
	for ev := range sub.C() {
		seen = append(seen, ev)
	}
	if len(seen) == 0 {
		t.Fatal("terminal event was not delivered in-band")
	}
	last, ok := seen[len(seen)-1].Value.(EventsLostEvent)
	if !ok {
		t.Fatalf("last value = %T, want EventsLostEvent", seen[len(seen)-1].Value)
	}
	if last.Reason != EventsLostReasonClosed {
		t.Fatalf("terminal reason = %q, want %q", last.Reason, EventsLostReasonClosed)
	}
	// Everything that never made it out counts as lost, so the number is not zero here.
	if last.LostCount == 0 {
		t.Fatal("LostCount = 0 despite unpumped backlog")
	}
}

// Overflow terminates the subscription instead of silently dropping the slow subscriber.
func TestSubscriberOverflowIsReported(t *testing.T) {
	cfg := testBrokerConfig()
	cfg.maxBacklog = 8
	cfg.stallTimeout = time.Minute // isolate overflow from stall
	b := newEventBroker(cfg)
	defer b.close()

	sub := b.Subscribe()
	for i := 0; i < cfg.maxBacklog*3; i++ {
		b.publish(Event{Method: "e"})
	}

	select {
	case <-sub.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("overflow never terminated the subscription")
	}
	got := lostError(t, sub)
	if got.Reason != EventsLostReasonOverflow {
		t.Fatalf("reason = %q, want %q", got.Reason, EventsLostReasonOverflow)
	}
	if got.LostCount <= 0 {
		t.Fatalf("LostCount = %d, want > 0", got.LostCount)
	}
}

// A consumer that resumes reading must be served again, and must not be mislabelled as
// stalled: the stall deadline resets on progress.
func TestSubscriberResumesAfterConsumerCatchesUp(t *testing.T) {
	cfg := testBrokerConfig()
	cfg.stallTimeout = 2 * time.Second
	b := newEventBroker(cfg)
	defer b.close()

	sub := b.Subscribe()

	// Fill past the buffer while nobody reads, then start reading.
	for i := 0; i < cfg.outCap*2; i++ {
		b.publish(Event{Method: "e"})
	}
	time.Sleep(100 * time.Millisecond)

	received := 0
	deadline := time.After(2 * time.Second)
drain:
	for received < cfg.outCap*2 {
		select {
		case <-sub.C():
			received++
		case <-deadline:
			break drain
		}
	}
	if received == 0 {
		t.Fatal("no events were delivered after the consumer resumed")
	}
	if err := sub.Err(); err != nil {
		t.Fatalf("subscription terminated while the consumer was keeping up: %v", err)
	}

	sub.Close()
	<-sub.Done()
	if got := lostError(t, sub); got.Reason != EventsLostReasonClosed {
		t.Fatalf("reason = %q, want %q (no spurious stall)", got.Reason, EventsLostReasonClosed)
	}
}

// One non-reading subscriber must not delay delivery to the others, and must not slow the
// publisher down as saturation persists.
func TestSlowSubscriberDoesNotAffectOthers(t *testing.T) {
	cfg := testBrokerConfig()
	cfg.stallTimeout = 5 * time.Second
	b := newEventBroker(cfg)
	defer b.close()

	slow := b.Subscribe() // never read
	defer slow.Close()
	fast := b.Subscribe()
	defer fast.Close()

	var wg sync.WaitGroup
	var got atomic.Int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range fast.C() {
			if got.Add(1) >= 50 {
				return
			}
		}
	}()

	const n = 500
	latencies := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		s := time.Now()
		b.publish(Event{Method: "e"})
		latencies = append(latencies, time.Since(s))
	}

	// The fast subscriber must have kept up without the slow one holding it back.
	deadline := time.Now().Add(2 * time.Second)
	for got.Load() < 50 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := got.Load(); n < 50 {
		t.Fatalf("fast subscriber only received %d events; the slow one blocked it", n)
	}

	// publish latency must not grow with saturation time: compare the first and last third.
	third := n / 3
	early := median(latencies[:third])
	late := median(latencies[n-third:])
	if late > early*20+2*time.Millisecond {
		t.Fatalf("publish latency grew with saturation: early=%v late=%v", early, late)
	}
	fast.Close()
}

func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	cp := append([]time.Duration(nil), d...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	return cp[len(cp)/2]
}

// Concurrent Close/publish/subscribe must be race-free (-race) and must not panic, which is
// the failure mode the old "close the channel from the publisher" design invited.
func TestSubscriptionConcurrentCloseAndPublish(t *testing.T) {
	b := newEventBroker(testBrokerConfig())
	defer b.close()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				b.publish(Event{Method: "e"})
			}
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				sub := b.Subscribe()
				sub.Close()
				sub.Close()
				<-sub.Done()
			}
		}()
	}
	wg.Wait()
}

// No subscribers may be left behind after Close: their forwarding goroutines must exit.
func TestClosingBrokerReleasesSubscribers(t *testing.T) {
	b := newEventBroker(testBrokerConfig())
	subs := make([]*EventSubscription, 0, 16)
	for i := 0; i < 16; i++ {
		subs = append(subs, b.Subscribe())
	}

	before := runtime.NumGoroutine()
	b.close()
	for _, s := range subs {
		select {
		case <-s.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("subscriber goroutine did not exit after broker close")
		}
		if got := lostError(t, s); got.Reason != EventsLostReasonClosed {
			t.Fatalf("reason = %q, want %q", got.Reason, EventsLostReasonClosed)
		}
	}

	// Allow the exiting goroutines to unwind before comparing.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine count grew: before=%d after=%d", before, after)
	}
}

// Subscribing to a closed broker must hand back an already-terminated subscription rather
// than a live one that never delivers.
func TestSubscribeAfterBrokerCloseIsTerminated(t *testing.T) {
	b := newEventBroker(testBrokerConfig())
	b.close()

	sub := b.Subscribe()
	select {
	case <-sub.Done():
	case <-time.After(time.Second):
		t.Fatal("subscription from a closed broker is not terminated")
	}
	if got := lostError(t, sub); got.Reason != EventsLostReasonClosed {
		t.Fatalf("reason = %q, want %q", got.Reason, EventsLostReasonClosed)
	}
}
