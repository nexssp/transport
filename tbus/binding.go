package tbus

import "github.com/nexssp/kernel/action"

// TopicBinding routes an internal memory event to an action.
type TopicBinding struct {
	Topic string
}

func (t *Transport) CanHandle(b action.Binding) bool {
	_, ok := b.(TopicBinding)
	return ok
}

func (t *Transport) String() string {
	return "bus"
}
func (b TopicBinding) String() string { return "Internal Bus: " + b.Topic }

// Topic binds an action to an in-process pub/sub topic.
func Topic(topic string) TopicBinding {
	return TopicBinding{Topic: topic}
}
