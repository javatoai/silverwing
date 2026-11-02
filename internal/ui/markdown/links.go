package markdownui

import (
	"fmt"
	"github.com/egoist/mygo/ui/markdown/parser"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type LinkTarget struct {
	Raw          string
	URL          string
	Path         string
	Fragment     string
	Line, Column int
}

// ResolveLink parses local paths before URL schemes so a Windows drive letter
// is never mistaken for an external protocol. Access is checked by the host.
func ResolveLink(sourcePath, destination string) (LinkTarget, error) {
	target := LinkTarget{Raw: destination}
	raw := strings.TrimSpace(destination)
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n") {
		return target, fmt.Errorf("链接地址无效")
	}
	drive := len(raw) >= 3 && unicode.IsLetter(rune(raw[0])) && raw[1] == ':' && (raw[2] == '/' || raw[2] == '\\')
	var local string
	if drive {
		u, err := url.Parse("file:///" + strings.ReplaceAll(raw, `\`, "/"))
		if err != nil {
			return target, fmt.Errorf("文件链接地址无效：%w", err)
		}
		local = strings.TrimPrefix(u.Path, "/")
		target.Fragment = u.Fragment
	} else {
		parseRaw := raw
		path := raw
		if at := strings.IndexAny(path, "?#"); at >= 0 {
			path = path[:at]
		}
		// A bare filename followed by a location looks like a URI scheme to
		// net/url. Only disambiguate a filename here; real schemes retain
		// their allowlist check below, including scheme:numeric-value forms.
		if base, line, _ := splitLocation(path); line > 0 && filepath.Ext(base) != "" && !strings.ContainsAny(base, `:/\`) {
			parseRaw = "./" + raw
		}
		u, err := url.Parse(strings.ReplaceAll(parseRaw, " ", "%20"))
		if err != nil {
			return target, fmt.Errorf("链接地址无效：%w", err)
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
			if u.Host == "" {
				return target, fmt.Errorf("网址缺少主机地址")
			}
			target.URL = u.String()
			return target, nil
		case "mailto":
			target.URL = u.String()
			return target, nil
		case "file":
			if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
				return target, fmt.Errorf("暂不支持网络文件链接")
			}
			local = u.Path
			if len(local) > 3 && local[0] == '/' && local[2] == ':' {
				local = local[1:]
			}
		default:
			if u.Scheme != "" || u.Host != "" {
				return target, fmt.Errorf("不支持此链接类型")
			}
			local = u.Path
		}
		target.Fragment = u.Fragment
		if local == "" && u.Fragment != "" {
			local = sourcePath
		}
	}
	if strings.HasPrefix(local, `\\`) || strings.HasPrefix(local, "//") {
		return target, fmt.Errorf("暂不支持网络文件链接")
	}
	local, target.Line, target.Column = splitLocation(local)
	if local == "" {
		return target, fmt.Errorf("链接没有文件路径")
	}
	localDrive := len(local) >= 3 && local[1] == ':' && (local[2] == '/' || local[2] == '\\')
	if !filepath.IsAbs(local) && !drive && !localDrive {
		if sourcePath == "" {
			return target, fmt.Errorf("当前文档没有位置，无法解析相对链接")
		}
		local = filepath.Join(filepath.Dir(sourcePath), filepath.FromSlash(local))
	}
	target.Path = filepath.Clean(local)
	if target.Line == 0 && len(target.Fragment) > 1 && (target.Fragment[0] == 'L' || target.Fragment[0] == 'l') {
		parts := strings.SplitN(target.Fragment[1:], ":", 2)
		line, err := strconv.Atoi(parts[0])
		if err == nil && line > 0 {
			target.Line = line
			if len(parts) == 2 {
				column, err := strconv.Atoi(parts[1])
				if err == nil && column > 0 {
					target.Column = column
				}
			}
		}
	}
	return target, nil
}

func splitLocation(path string) (string, int, int) {
	last := strings.LastIndexByte(path, ':')
	if last <= 1 {
		return path, 0, 0
	}
	n, err := strconv.Atoi(path[last+1:])
	if err != nil || n <= 0 {
		return path, 0, 0
	}
	prefix := path[:last]
	previous := strings.LastIndexByte(prefix, ':')
	if previous > 1 {
		if line, e := strconv.Atoi(prefix[previous+1:]); e == nil && line > 0 {
			return prefix[:previous], line, n
		}
	}
	return prefix, n, 0
}

func escapeLabel(label string) string {
	return strings.NewReplacer("&", "&amp;", "\\", "\\\\", "[", "\\[", "]", "\\]", "|", "\\|", "\n", " ", "\r", " ").Replace(label)
}
func linkSource(label, destination string) string {
	return "[" + escapeLabel(label) + "](<" + parser.EscapeLinkDestination(destination) + ">)"
}
