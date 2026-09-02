package delivery

import (
	"context"
	"fmt"
	"sync"
)

// Sent records one call to FakeDelivery.Send.
type Sent struct {
	Destination string
	Message     string
}

// FakeDelivery is the only Delivery this repository wires up by default —
// it never contacts Telegram. Tests (and `monitor`-style local
// verification without a bot token) use it to observe exactly what the
// scheduler would have sent.
type FakeDelivery struct {
	mu       sync.Mutex
	sent     []Sent
	failNext int
	err      error
}

func NewFakeDelivery() *FakeDelivery { return &FakeDelivery{} }

func (f *FakeDelivery) Send(_ context.Context, destination, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext > 0 {
		f.failNext--
		if f.err != nil {
			return f.err
		}
		return fmt.Errorf("fake delivery: simulated transient failure")
	}
	f.sent = append(f.sent, Sent{Destination: destination, Message: message})
	return nil
}

// FailNext makes the next n Send calls fail with err (or a generic error if
// err is nil).
func (f *FakeDelivery) FailNext(n int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = n
	f.err = err
}

func (f *FakeDelivery) All() []Sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Sent, len(f.sent))
	copy(out, f.sent)
	return out
}
