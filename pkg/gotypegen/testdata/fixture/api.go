package fixture

// APIResponse is a generic API response wrapper.
type APIResponse[T any] struct {
	Success bool   `json:"success"`
	Data    T      `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// SDKTypes is a phantom type that pulls types into the trace.
type SDKTypes struct {
	_ App
	_ TaskStatus
}
