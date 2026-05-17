package fixture

import (
	"github.com/inference-sh/gotypegen/pkg/gotypegen/testdata/fixture/shared"
)

// Document uses types from the shared package.
type Document struct {
	Base       `json:",inline" tstype:",extends" gorm:"embedded"`
	Title      string            `json:"title"`
	Visibility shared.Visibility `json:"visibility"`
	Status     shared.Status     `json:"status"`
	Cover      *shared.FileRef   `json:"cover,omitempty"`
}

// IsPublic checks if the document is publicly visible.
func (d Document) IsPublic() bool {
	return d.Visibility == shared.VisibilityPublic
}
