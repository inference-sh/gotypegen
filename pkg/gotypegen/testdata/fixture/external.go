package fixture

import "encoding/json"

// MarshalApp is a method that uses an external-ish package.
// For testing purposes, json is stdlib so this SHOULD be included.
func (a App) MarshalApp() ([]byte, error) {
	return json.Marshal(a)
}
