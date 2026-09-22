package fixture

import "encoding/json"

// Settings is a named map: nil encodes as JSON null exactly like the bare
// map it wraps, so non-Go consumers must treat fields of this type as nullable.
type Settings map[string]any

// Bound marshals as either a bare literal or {"path": ...} — its wire shape
// is whatever MarshalJSON writes, not its struct fields.
type Bound struct {
	Literal any    `json:"-"`
	Path    string `json:"path,omitempty"`
}

func (b Bound) MarshalJSON() ([]byte, error) {
	if b.Path != "" {
		return json.Marshal(struct {
			Path string `json:"path"`
		}{b.Path})
	}
	return json.Marshal(b.Literal)
}

type WireShapes struct {
	Settings Settings `json:"settings"`
	Label    *Bound   `json:"label,omitempty"`
	Value    Bound    `json:"value"`
}
