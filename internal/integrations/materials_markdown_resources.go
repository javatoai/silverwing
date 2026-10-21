package integrations

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var errMaterialsTextCitation = errors.New("unmapped Markdown reference should remain a text citation")

var (
	materialsResourceScheme      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	materialsExternalScheme      = regexp.MustCompile(`(?i)^(https?://|mailto:)`)
	materialsWindowsAbsolute     = regexp.MustCompile(`^[A-Za-z]:[/\\]`)
	materialsCitationPosition    = regexp.MustCompile(`:\d+(?::\d+)?$`)
	materialsResourceCode        = regexp.MustCompile("`+[^`]*`+")
	materialsImageReference      = regexp.MustCompile(`!\[([^]]*)](?:\[([^]]*)])?`)
	materialsReferenceDefinition = regexp.MustCompile(`^([\t\n\v\f\r ]*\[([^]]+)]:[\t\n\v\f\r ]*)(<[^>]+>|[^\t\n\v\f\r ]+)(.*)$`)
	materialsFullReference       = regexp.MustCompile(`\[([^]]+)]\[([^]]*)]`)
	materialsShortcutReference   = regexp.MustCompile(`\[([^]]+)]`)
	materialsInlineResource      = regexp.MustCompile(`(!?\[([^]]*)]\()( <[^>]+>|<[^>]+>|[^\t\n\v\f\r )]+)([^)]*\))`)
	materialsHTMLImage           = regexp.MustCompile(`(?i)(<(?:img|image)\b[^>]*?\bsrc[\t\n\v\f\r ]*=[\t\n\v\f\r ]*["'])([^"']+)(["'][^>]*>)`)
)

// The Source scanner and uploader use one resource walker. Keep that contract:
// code is literal, local resources share validation, outside text links become
// citations, and only supported resource spans are replaced.
func rewriteMaterialsMarkdownResources(root, relative, content string, replace func(string, bool) (string, error)) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	resource := func(raw string, image bool) (*string, error) {
		unwrapped := materialsUnwrapResource(raw)
		if strings.HasPrefix(unwrapped, "#") || materialsExternalScheme.MatchString(unwrapped) {
			return &raw, nil
		}
		outside := func() (*string, error) {
			if image {
				return nil, fmt.Errorf("图片引用超出任务资料目录：%s（来源：%s）", raw, relative)
			}
			return nil, nil
		}
		anchor := ""
		if index := strings.IndexByte(unwrapped, '#'); index >= 0 {
			anchor = unwrapped[index+1:]
		}
		pathname := strings.SplitN(strings.SplitN(unwrapped, "#", 2)[0], "?", 2)[0]
		if strings.HasPrefix(pathname, "//") || strings.HasPrefix(pathname, `\\`) {
			return outside()
		}
		fileURI := strings.HasPrefix(strings.ToLower(pathname), "file:")
		if !fileURI && !materialsWindowsAbsolute.MatchString(pathname) && materialsResourceScheme.MatchString(pathname) {
			return nil, fmt.Errorf("不支持的资料引用：%s（来源：%s）", raw, relative)
		}
		encoded := pathname
		if fileURI {
			parsed, err := url.Parse(pathname)
			if err != nil || parsed.Path == "" {
				return nil, fmt.Errorf("本地文件引用无效：%s（来源：%s）", raw, relative)
			}
			if parsed.User != nil || parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
				return outside()
			}
			encoded = parsed.EscapedPath()
		}
		decoded, err := url.PathUnescape(encoded)
		if err != nil {
			return nil, fmt.Errorf("资料引用无效：%s（来源：%s）", raw, relative)
		}
		if fileURI && materialsWindowsAbsolute.MatchString(strings.TrimPrefix(decoded, "/")) {
			decoded = strings.TrimPrefix(decoded, "/")
		}
		if materialsWindowsAbsolute.MatchString(decoded) && filepath.Separator != '\\' {
			return outside()
		}
		if !image && filepath.Separator == '\\' {
			decoded = materialsCitationPosition.ReplaceAllString(decoded, "")
		}
		resolved := filepath.FromSlash(decoded)
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(filepath.Join(root, filepath.FromSlash(relative))), resolved)
		}
		resolved = filepath.Clean(resolved)
		path, err := filepath.Rel(root, resolved)
		if err != nil || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
			return outside()
		}
		if err := requirementSafePath(root, resolved); err != nil {
			return nil, fmt.Errorf("资料引用路径无效：%s：%w", raw, err)
		}
		real, err := filepath.EvalSymlinks(resolved)
		if err != nil || filepath.Clean(real) != resolved {
			return nil, fmt.Errorf("资料引用不存在或包含链接：%s（来源：%s）", raw, relative)
		}
		path = filepath.ToSlash(path)
		for _, name := range strings.Split(path, "/") {
			if excludedMaterialsName(name) {
				return nil, fmt.Errorf("不能引用 SilverWing 控制文件：%s", path)
			}
		}
		target, err := replace(path, image)
		if err != nil {
			if !image && errors.Is(err, errMaterialsTextCitation) {
				return outside()
			}
			return nil, err
		}
		if !image && anchor != "" {
			target += "#" + anchor
		}
		return &target, nil
	}
	imageReferences := map[string]bool{}
	for _, match := range materialsImageReference.FindAllStringSubmatchIndex(content, -1) {
		id := content[match[2]:match[3]]
		if match[4] >= 0 {
			if value := content[match[4]:match[5]]; trimRequirementText(value) != "" {
				id = value
			}
		} else if match[1] < len(content) && (content[match[1]] == '[' || content[match[1]] == '(') {
			continue
		}
		imageReferences[strings.ToLower(id)] = true
	}
	type definition struct {
		raw    string
		target *string
	}
	definitions := map[string]definition{}
	_, err = materialsMarkdownText(content, func(text string) (string, error) {
		if match := materialsReferenceDefinition.FindStringSubmatch(text); match != nil {
			id := strings.ToLower(match[2])
			target, err := resource(match[3], imageReferences[id])
			if err != nil {
				return "", err
			}
			definitions[id] = definition{raw: match[3], target: target}
		}
		return text, nil
	})
	if err != nil {
		return "", err
	}
	return materialsMarkdownText(content, func(text string) (string, error) {
		if match := materialsReferenceDefinition.FindStringSubmatch(text); match != nil {
			target := definitions[strings.ToLower(match[2])].target
			if target == nil {
				return "", nil
			}
			return match[1] + *target + match[4], nil
		}
		text, err := materialsReplaceResourceMatches(text, materialsFullReference, func(text string, match []int) (string, error) {
			if match[0] > 0 && text[match[0]-1] == '!' {
				return text[match[0]:match[1]], nil
			}
			label, id := text[match[2]:match[3]], text[match[4]:match[5]]
			if trimRequirementText(id) == "" {
				id = label
			}
			if definition, ok := definitions[strings.ToLower(id)]; ok && definition.target == nil {
				return materialsResourceCitation(label, definition.raw), nil
			}
			return text[match[0]:match[1]], nil
		})
		if err != nil {
			return "", err
		}
		text, err = materialsReplaceResourceMatches(text, materialsShortcutReference, func(text string, match []int) (string, error) {
			if match[0] > 0 && text[match[0]-1] == '!' || match[1] < len(text) && (text[match[1]] == '[' || text[match[1]] == '(') {
				return text[match[0]:match[1]], nil
			}
			label := text[match[2]:match[3]]
			if definition, ok := definitions[strings.ToLower(label)]; ok && definition.target == nil {
				return materialsResourceCitation(label, definition.raw), nil
			}
			return text[match[0]:match[1]], nil
		})
		if err != nil {
			return "", err
		}
		text, err = materialsReplaceResourceMatches(text, materialsInlineResource, func(text string, match []int) (string, error) {
			prefix, label, raw, suffix := text[match[2]:match[3]], text[match[4]:match[5]], trimRequirementText(text[match[6]:match[7]]), text[match[8]:match[9]]
			target, err := resource(raw, strings.HasPrefix(prefix, "!"))
			if err != nil {
				return "", err
			}
			if target == nil {
				return materialsResourceCitation(label, raw), nil
			}
			return prefix + *target + suffix, nil
		})
		if err != nil {
			return "", err
		}
		return materialsReplaceResourceMatches(text, materialsHTMLImage, func(text string, match []int) (string, error) {
			target, err := resource(text[match[4]:match[5]], true)
			if err != nil {
				return "", err
			}
			return text[match[2]:match[3]] + strings.TrimSuffix(strings.TrimPrefix(*target, "<"), ">") + text[match[6]:match[7]], nil
		})
	})
}

func materialsUnwrapResource(raw string) string {
	if strings.HasPrefix(raw, "<") && strings.HasSuffix(raw, ">") {
		raw = raw[1 : len(raw)-1]
	}
	return strings.TrimPrefix(raw, "@")
}

func materialsResourceCitation(label, raw string) string {
	path := materialsUnwrapResource(raw)
	longest, current := 0, 0
	for _, char := range path {
		if char == '`' {
			current++
			if current > longest {
				longest = current
			}
		} else {
			current = 0
		}
	}
	ticks := strings.Repeat("`", longest+1)
	return label + "（" + ticks + path + ticks + "）"
}

func materialsReplaceResourceMatches(text string, pattern *regexp.Regexp, replace func(string, []int) (string, error)) (string, error) {
	var out strings.Builder
	cursor := 0
	for _, match := range pattern.FindAllStringSubmatchIndex(text, -1) {
		out.WriteString(text[cursor:match[0]])
		value, err := replace(text, match)
		if err != nil {
			return "", err
		}
		out.WriteString(value)
		cursor = match[1]
	}
	out.WriteString(text[cursor:])
	return out.String(), nil
}

// Match Source's fence/indent/code-span rules while retaining original newline
// bytes and every untouched source span instead of reserializing the document.
func materialsMarkdownText(content string, rewrite func(string) (string, error)) (string, error) {
	var out strings.Builder
	var fence byte
	fenceLength := 0
	for start := 0; start < len(content); {
		end := start
		for end < len(content) && content[end] != '\r' && content[end] != '\n' {
			end++
		}
		line := content[start:end]
		stripped := trimRequirementText(line)
		if !strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "\t") && (strings.HasPrefix(stripped, "```") || strings.HasPrefix(stripped, "~~~")) {
			mark := stripped[0]
			length := 0
			for length < len(stripped) && stripped[length] == mark {
				length++
			}
			if fence == mark && length >= fenceLength && strings.TrimSpace(stripped[length:]) == "" {
				fence = 0
				fenceLength = 0
			} else if fence == 0 {
				fence = mark
				fenceLength = length
			}
			out.WriteString(line)
		} else if fence != 0 || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			out.WriteString(line)
		} else {
			cursor := 0
			for _, match := range materialsResourceCode.FindAllStringIndex(line, -1) {
				value, err := rewrite(line[cursor:match[0]])
				if err != nil {
					return "", err
				}
				out.WriteString(value)
				out.WriteString(line[match[0]:match[1]])
				cursor = match[1]
			}
			value, err := rewrite(line[cursor:])
			if err != nil {
				return "", err
			}
			out.WriteString(value)
		}
		start = end
		if start < len(content) {
			out.WriteByte(content[start])
			start++
			if content[start-1] == '\r' && start < len(content) && content[start] == '\n' {
				out.WriteByte('\n')
				start++
			}
		}
	}
	return out.String(), nil
}
