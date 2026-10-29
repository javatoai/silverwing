// Package markdowntext contains pure, source-preserving Markdown syntax operations.
// It never executes commands, accesses resources, or owns live editor state.
package markdowntext

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
