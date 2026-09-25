package enums

// Color has few values.
type Color string

const (
	ColorRed  Color = "red"
	ColorBlue Color = "blue"
)

// ColorCrimson repeats a value and lives outside the group.
const ColorCrimson Color = "red"

// Stage has enough values to wrap.
type Stage string

const (
	StageQueued  Stage = "queued"
	StageRunning Stage = "running"
	StageDone    Stage = "done"
	StageFailed  Stage = "failed"
	StageSkipped Stage = "skipped"
)

// Label has no consts and stays a string.
type Label string

// Name is an alias and stays a string.
type Name = string

const NameDefault Name = "default"

// Governance values are untyped, so tracing drops them.
const (
	GovernedBySelf = "self"
	GovernedByOrg  = "org"
)

// MaxItems is a lone untyped const.
const MaxItems = "10"

const (
	limitA = "a"
	limitB = "b"
)

type Item struct {
	Color Color  `json:"color"`
	Stage Stage  `json:"stage"`
	Label Label  `json:"label"`
	Name  Name   `json:"name"`
	Owner string `json:"owner"`
}
