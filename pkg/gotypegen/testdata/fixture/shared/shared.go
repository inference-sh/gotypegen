package shared

// Visibility controls who can see a resource.
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
	VisibilityTeam    Visibility = "team"
)

// Status is a generic status enum.
type Status int

const (
	StatusActive  Status = iota
	StatusPaused
	StatusDeleted
)

// String returns the string representation.
func (s Status) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusPaused:
		return "paused"
	case StatusDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// FileRef is a reference to a file.
type FileRef struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
}
