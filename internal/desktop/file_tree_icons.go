package desktop

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

func fileTreeIcon(row FileTreeRow, expanded bool, p palette) (string, ui.Color) {
	if row.Directory {
		if expanded {
			return "folder-open", p.muted
		}
		return "folder", p.muted
	}
	ext := strings.ToLower(filepath.Ext(row.Name))
	switch ext {
	case ".md", ".markdown", ".mdown":
		return "file-markdown", p.blue
	case ".txt", ".text", ".log", ".csv", ".tsv", ".ini", ".conf", ".config", ".env", ".properties":
		return "file", p.muted
	case ".json", ".jsonc", ".jsonl":
		return "file-json", p.amber
	case ".sql":
		return "file-sql", p.blue
	case ".go", ".java", ".kt", ".kts", ".js", ".jsx", ".ts", ".tsx", ".py", ".rs", ".c", ".h", ".cpp", ".cs", ".swift", ".sh", ".ps1", ".bat", ".cmd", ".html", ".css", ".scss", ".xml", ".yaml", ".yml", ".toml", ".vue", ".rb", ".php", ".lua", ".r":
		return "file-code", p.purple
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".ico", ".avif", ".tif", ".tiff":
		return "file-image", p.green
	case ".pdf":
		return "file-pdf", p.danger
	default:
		return "file-unknown", p.muted
	}
}
