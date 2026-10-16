package tmsg

// TopicBinding routes a Kernel action from the canonical message envelope.
type TopicBinding struct {
	Topic string
	s     string // "Message: " + Topic, computed once in Topic()
}

func Topic(topic string) TopicBinding {
	return TopicBinding{Topic: topic, s: "Message: " + topic}
}

func (b TopicBinding) String() string { return b.s }
