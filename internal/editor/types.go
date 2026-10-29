// Package editor contains the source-preserving Markdown document model used
// by native desktop renderers. All ranges are half-open UTF-8 byte offsets in
// the original document, so untouched source slices can be written back byte
// for byte (including CRLF line endings).
package editor

import (
	"time"

	"silverwing-go/internal/markdowntext"
)

const MaxDocumentBytes = 3 * 1024 * 1024

type PartKind = markdowntext.PartKind

const (
	PartRich     = markdowntext.PartRich
	PartCode     = markdowntext.PartCode
	PartMermaid  = markdowntext.PartMermaid
	PartTable    = markdowntext.PartTable
	PartTasks    = markdowntext.PartTasks
	PartImage    = markdowntext.PartImage
	PartOriginal = markdowntext.PartOriginal
	PartMetadata = markdowntext.PartMetadata
	PartDivider  = markdowntext.PartDivider
)

type SourceRange = markdowntext.SourceRange
type Node = markdowntext.Node
type Part = markdowntext.Part

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

type TableCell = markdowntext.TableCell
type Table = markdowntext.Table

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
