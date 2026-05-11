package fixture

import "github.com/fatih/structtag"

// Ensure import is used
var _ = structtag.Tags{}

// ParseTags uses an external (non-stdlib) package — should be filtered out.
func (a App) ParseTags() *structtag.Tags {
	return nil
}
