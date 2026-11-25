package thttp

// ListenerReadyEvent describes a successfully bound HTTP listener.
type ListenerReadyEvent struct {
	BindAddress string `json:"bind_address"`
}
