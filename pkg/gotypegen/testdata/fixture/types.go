package fixture

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Ensure imports are used
var (
	_ = json.Marshal
	_ = fmt.Sprintf
	_ = strings.TrimSpace
	_ = time.Now
)

// AppCategory represents the category of an app.
type AppCategory string

const (
	AppCategoryImage AppCategory = "image"
	AppCategoryVideo AppCategory = "video"
	AppCategoryAudio AppCategory = "audio"
	AppCategoryText  AppCategory = "text"
)

// Base holds common fields for all models.
type Base struct {
	ID        string     `json:"id" gorm:"primaryKey" yaml:"id"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime" yaml:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`
}

// App represents an application.
type App struct {
	Base     `json:",inline" tstype:",extends" gorm:"embedded"`
	Name     string      `json:"name" gorm:"not null" validate:"required"`
	Category AppCategory `json:"category" validate:"oneof=image video audio text"`
	Version  *AppVersion `json:"version,omitempty"`
	Tags     []string    `json:"tags,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// FullName returns the display name of the app.
func (a App) FullName() string {
	return strings.TrimSpace(a.Name)
}

// Ref returns a reference string for the app.
func (a *App) Ref() string {
	if a.Version != nil {
		return fmt.Sprintf("%s@%s", a.Name, a.Version.Tag)
	}
	return a.Name
}

// AppVersion represents a version of an app.
type AppVersion struct {
	Tag         string `json:"tag" gorm:"not null"`
	Description string `json:"description,omitempty"`
}

// String returns the tag.
func (v AppVersion) String() string {
	return v.Tag
}

// TaskStatus represents the status of a task.
type TaskStatus int

const (
	TaskStatusQueued     TaskStatus = iota
	TaskStatusRunning
	TaskStatusCompleted
	TaskStatusFailed
)

// String returns the string representation.
func (ts TaskStatus) String() string {
	switch ts {
	case TaskStatusQueued:
		return "queued"
	case TaskStatusRunning:
		return "running"
	case TaskStatusCompleted:
		return "completed"
	case TaskStatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// unexportedType should not appear in output.
type unexportedType struct {
	field string
}

// Unreferenced is not referenced by any entry type.
type Unreferenced struct {
	Data string `json:"data"`
}

// GetUnreferenced returns an Unreferenced — should be filtered out in trace mode
// because Unreferenced is not in the trace set.
func (a App) GetUnreferenced() *Unreferenced {
	return &Unreferenced{Data: a.Name}
}

// --- Fixtures for method filtering edge cases ---

// helperFunc is a package-level function (not emitted in trace mode).
func helperFunc() string {
	return "helper"
}

// statusLabels is a package-level var (not emitted in trace mode).
var statusLabels = map[TaskStatus]string{
	TaskStatusQueued: "Queued",
}

// UsesHelper calls a package-level function — should be filtered in trace mode.
func (a App) UsesHelper() string {
	return helperFunc()
}

// UsesVar references a package-level var — should be filtered in trace mode.
func (ts TaskStatus) Label() string {
	if l, ok := statusLabels[ts]; ok {
		return l
	}
	return "unknown"
}

// IsTerminal checks if status is terminal.
// References a package-level var, so filtered in trace mode.
func (ts TaskStatus) IsTerminal() bool {
	return statusLabels[ts] != ""
}

// CanTransition calls IsTerminal which itself gets filtered — cascading filter.
func (ts TaskStatus) CanTransition(next TaskStatus) bool {
	if ts.IsTerminal() {
		return false
	}
	return true
}

// MarshalLocal uses a local type declaration inside the method body.
func (a App) MarshalLocal() ([]byte, error) {
	type Local struct {
		Name string `json:"name"`
	}
	return json.Marshal(Local{Name: a.Name})
}
