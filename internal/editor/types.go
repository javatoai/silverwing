// Package editor contains the source-preserving Markdown document model used
// by native desktop renderers. All ranges are half-open UTF-8 byte offsets in
// the original document, so untouched source slices can be written back byte
// for byte (including CRLF line endings).
package editor

import "time"

const MaxDocumentBytes = 3 * 1024 * 1024

type PartKind string

const (
	PartRich     PartKind = "RICH"
	PartCode     PartKind = "CODE"
	PartMermaid  PartKind = "MERMAID"
	PartTable    PartKind = "TABLE"
	PartTasks    PartKind = "TASKS"
	PartImage    PartKind = "IMAGE"
	PartOriginal PartKind = "ORIGINAL"
	PartMetadata PartKind = "METADATA"
	PartDivider  PartKind = "DIVIDER"
)

// SourceRange is half-open [Start, End), measured in UTF-8 bytes.
type SourceRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Node is a source-located Markdown AST node. Unknown constructs are emitted
// as original nodes instead of being normalized or dropped.
type Node struct {
	Kind        string            `json:"kind"`
	Range       SourceRange       `json:"range"`
	Text        string            `json:"text,omitempty"`
	Destination string            `json:"destination,omitempty"`
	Title       string            `json:"title,omitempty"`
	Language    string            `json:"language,omitempty"`
	Level       int               `json:"level,omitempty"`
	Ordered     bool              `json:"ordered,omitempty"`
	Checked     *bool             `json:"checked,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Children    []Node            `json:"children,omitempty"`
}

type Part struct {
	ID     uint64      `json:"id"`
	Kind   PartKind    `json:"kind"`
	Range  SourceRange `json:"range"`
	Source string      `json:"source"`
	Nodes  []Node      `json:"nodes,omitempty"`
}

type Cursor struct {
	Part  uint64 `json:"part"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Field string `json:"field,omitempty"`
}

type Selection struct {
	First uint64 `json:"first"`
	Last  uint64 `json:"last"`
}

type Snapshot struct {
	DocumentID     string          `json:"documentId"`
	Source         string          `json:"source"`
	Parts          []Part          `json:"parts"`
	Version        uint64          `json:"version"`
	FocusRequest   uint64          `json:"focusRequest"`
	Cursor         *Cursor         `json:"cursor,omitempty"`
	Selection      *Selection      `json:"selection,omitempty"`
	TableSelection *TableSelection `json:"tableSelection,omitempty"`
	Composing      bool            `json:"composing"`
	Error          string          `json:"error,omitempty"`
	CanUndo        bool            `json:"canUndo"`
	CanRedo        bool            `json:"canRedo"`
}

type FormatCommand string

const (
	FormatBold          FormatCommand = "bold"
	FormatItalic        FormatCommand = "italic"
	FormatStrike        FormatCommand = "strike"
	FormatInlineCode    FormatCommand = "inline-code"
	FormatLink          FormatCommand = "link"
	FormatHeading       FormatCommand = "heading"
	FormatUnorderedList FormatCommand = "unordered-list"
	FormatOrderedList   FormatCommand = "ordered-list"
)

type FormatRequest struct {
	PartID  uint64        `json:"partId"`
	Start   int           `json:"start"`
	End     int           `json:"end"`
	Command FormatCommand `json:"command"`
	Value   string        `json:"value,omitempty"`
	Level   int           `json:"level,omitempty"`
}

type TableCell struct {
	Row    int         `json:"row"`
	Column int         `json:"column"`
	Range  SourceRange `json:"range"`
	Text   string      `json:"text"`
}

type Table struct {
	Rows      [][]string `json:"rows"`
	Alignment []string   `json:"alignment"`
}

type SaveSnapshot struct {
	Revision uint64    `json:"revision"`
	Content  string    `json:"content"`
	Captured time.Time `json:"capturedAt"`
}

type SaveState struct {
	Revision           uint64 `json:"revision"`
	SavedRevision      uint64 `json:"savedRevision"`
	DraftSavedRevision uint64 `json:"draftSavedRevision"`
	Dirty              bool   `json:"dirty"`
	Saving             bool   `json:"saving"`
	Error              string `json:"error,omitempty"`
	CacheError         string `json:"cacheError,omitempty"`
	Conflict           bool   `json:"conflict"`
	Composing          bool   `json:"composing"`
}

type FlushFailure struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}
