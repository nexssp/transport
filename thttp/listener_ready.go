package thttp

// ListenerReadyEvent describes a successfully bound HTTP listener.
type ListenerReadyEvent struct {
	ServiceID     string `json:"service_id"`
	AdvertisedURL string `json:"advertised_url"`
	BindAddress   string `json:"bind_address"`
}
