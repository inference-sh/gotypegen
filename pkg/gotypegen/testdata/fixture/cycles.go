package fixture

// Thread reaches itself through a pointer (Parent) and through a slice
// (Replies). Only the pointer closes a cycle a Swift struct can't hold.
type Thread struct {
	ID      string    `json:"id"`
	Parent  *Thread   `json:"parent,omitempty"`
	Replies []*Thread `json:"replies,omitempty"`
}

// Folder and Note reach each other: Folder → *Note → *Folder.
type Folder struct {
	Name   string `json:"name"`
	Pinned *Note  `json:"pinned,omitempty"`
}

type Note struct {
	Text   string  `json:"text"`
	Folder *Folder `json:"folder,omitempty"`
}
