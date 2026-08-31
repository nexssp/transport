package thttp

import (
	"bytes"
	"sync"
)

const defaultSubscriberBuffer = 16

type StreamBroadcaster interface {
	Subscribe(channel string) (<-chan []byte, func())
	Publish(channel string, payload []byte) (delivered int, dropped int)
}

type streamTopic struct {
	subs map[chan []byte]struct{}
}

type InMemoryBroadcaster struct {
	mu     sync.RWMutex
	topics map[string]*streamTopic
	buffer int
}

func NewBroadcaster(buffer int) *InMemoryBroadcaster {
	if buffer <= 0 {
		buffer = defaultSubscriberBuffer
	}
	return &InMemoryBroadcaster{
		topics: make(map[string]*streamTopic),
		buffer: buffer,
	}
}

func (b *InMemoryBroadcaster) Subscribe(channel string) (<-chan []byte, func()) {
	ch := make(chan []byte, b.buffer)

	b.mu.Lock()
	topic := b.topics[channel]
	if topic == nil {
		topic = &streamTopic{subs: make(map[chan []byte]struct{})}
		b.topics[channel] = topic
	}
	topic.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.unsubscribe(channel, ch)
		})
	}
	return ch, cancel
}

func (b *InMemoryBroadcaster) unsubscribe(channel string, ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	topic := b.topics[channel]
	if topic == nil {
		return
	}
	delete(topic.subs, ch)
	if len(topic.subs) == 0 {
		delete(b.topics, channel)
	}
	close(ch)
}

func (b *InMemoryBroadcaster) Publish(channel string, payload []byte) (delivered int, dropped int) {
	b.mu.RLock()
	topic := b.topics[channel]
	if topic == nil {
		b.mu.RUnlock()
		return 0, 0
	}

	ownedPayload := bytes.Clone(payload)
	for ch := range topic.subs {
		select {
		case ch <- ownedPayload:
			delivered++
		default:
			dropped++
		}
	}
	b.mu.RUnlock()
	return delivered, dropped
}
