package pykeys

// Hook has a JSON key that is a Python keyword.
type Hook struct {
	Event string `json:"event"`
	// Async runs the hook without blocking.
	Async   bool `json:"async,omitempty"`
	Timeout int  `json:"timeout,omitempty"`
}

// Delta carries merge tags.
type Delta struct {
	Index     int    `json:"index"`
	Name      string `json:"name,omitempty" merge:"replace"`
	Arguments string `json:"arguments,omitempty" merge:"concat"`
}

// KeywordDelta has both a keyword key and merge tags.
type KeywordDelta struct {
	From string `json:"from,omitempty" merge:"concat"`
}
