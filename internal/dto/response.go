package dto

// HealthResponse represents the aggregated health check response.
type HealthResponse struct {
	Gateway        string `json:"gateway"`
	PixoraBackend  string `json:"pixora_backend"`
	ClockwerkMedia string `json:"clockwerk_media"`
	Redis          string `json:"redis"`
}
