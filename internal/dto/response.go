package dto

// HealthResponse represents the aggregated health check response.
type HealthResponse struct {
	Gateway string `json:"gateway"`
	Backend string `json:"backend"`
	Redis   string `json:"redis"`
}
