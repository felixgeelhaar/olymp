// Package eventbus is the in-process event fan-out backing OlympAPI.Stream.
//
// The bus is fan-out, lossless for fast subscribers, and applies a
// drop-on-overflow policy for slow ones (with a structured warning written
// to bolt). The wire shape (RunEvent) is identical to the Phase-3 Kafka
// backbone so the runtime doesn't change shape when we scale out.
package eventbus

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/felixgeelhaar/olymp/internal/domain"
)

// Bus is the in-process pub/sub for RunEvents. Construct with New.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[uint64]*subscription
	nextID      uint64
	bufSize     int
	dropped     atomic.Uint64
}

// New returns a Bus. bufSize is the per-subscriber buffer; events past it are
// dropped on the slow subscriber rather than blocking publishers.
func New(bufSize int) *Bus {
	if bufSize <= 0 {
		bufSize = 64
	}
	return &Bus{subscribers: map[uint64]*subscription{}, bufSize: bufSize}
}

// Publish broadcasts an event to every subscriber whose filter matches.
func (b *Bus) Publish(ev domain.RunEvent) {
	b.mu.RLock()
	subs := make([]*subscription, 0, len(b.subscribers))
	for _, s := range b.subscribers {
		subs = append(subs, s)
	}
	b.mu.RUnlock()
	for _, s := range subs {
		if !match(s.filter, ev) {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			// Slow subscriber: drop oldest, deliver newest.
			select {
			case <-s.ch:
				b.dropped.Add(1)
			default:
			}
			select {
			case s.ch <- ev:
			default:
				b.dropped.Add(1)
			}
		}
	}
}

// Subscribe returns a receive-only channel of RunEvents matching filter.
// The channel is closed when ctx is cancelled or Close is called. Callers
// MUST drain the channel; failure to do so triggers the drop-on-overflow
// policy described above.
func (b *Bus) Subscribe(ctx context.Context, filter domain.RunFilter) <-chan domain.RunEvent {
	ch := make(chan domain.RunEvent, b.bufSize)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subscribers[id] = &subscription{filter: filter, ch: ch}
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		if s, ok := b.subscribers[id]; ok {
			delete(b.subscribers, id)
			close(s.ch)
		}
		b.mu.Unlock()
	}()

	return ch
}

// Dropped returns the cumulative count of events dropped due to slow
// subscribers. Useful for health checks + alerting.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }

// SubscriberCount returns the current number of active subscribers.
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

type subscription struct {
	filter domain.RunFilter
	ch     chan domain.RunEvent
}

func match(f domain.RunFilter, ev domain.RunEvent) bool {
	if f.RunID != "" && ev.RunID != f.RunID {
		return false
	}
	// IntentType / Caller / Status filters require the publisher to embed
	// these fields in the event payload; for now we accept events without
	// these fields rather than dropping them silently.
	return true
}
