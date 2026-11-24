package genbuapi

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var (
	approvalInlineAssignment = regexp.MustCompile(`(?i)(["']?([a-z0-9_.-]+)["']?[ \t]*[:=][ \t]*)("(?:\\.|[^"\\])*"|'(?:''|[^'])*'|[^\s,;\[\]{}]+)`)
	approvalInlineContainer  = regexp.MustCompile(`(?i)["']?([a-z0-9_.-]+)["']?[ \t]*[:=][ \t]*[\[{]`)
	approvalNamedSetting     = regexp.MustCompile(`(?im)^\s*(?:-\s*)?(?:name|key)[ \t]*:[ \t]*["']?([a-z0-9_.-]+)["']?[ \t]*(?:#[^\r\n]*)?$`)
	approvalBasicAuth        = regexp.MustCompile(`(?i)(\b(?:proxy-)?authorization["']?[ \t]*[:=][ \t]*["']?)Basic[ \t]+[a-z0-9+/_=-]+`)
	approvalSpaceProperty    = regexp.MustCompile(`^[ \t]*([a-zA-Z0-9_.-]+)[ \t]+[^ \t:=]`)
	approvalQueryParameter   = regexp.MustCompile(`([?&;])([^?&;#=\s"']+)=([^&#;\s"']+)`)
)

func approvalDetailFields(row map[string]any, exactSecrets ...string) map[string]string {
	fields := approvalDetailMetadata(row, exactSecrets)

	content, _ := row["content"].(map[string]any)
	switch valueString(row, "approve_type") {
	case "2":
		addApprovalFields(fields, content, map[string]string{"name": "Service 名称"}, exactSecrets)
		ports, _ := content["port_data"].([]any)
		for index, value := range ports {
			port, _ := value.(map[string]any)
			addApprovalFields(fields, port, map[string]string{
				"service_port":   fmt.Sprintf("服务端口 %d", index+1),
				"container_port": fmt.Sprintf("容器端口 %d", index+1),
				"agreement":      fmt.Sprintf("端口协议 %d", index+1),
			}, exactSecrets)
		}
	case "3":
		addApprovalFields(fields, content, map[string]string{"name": "Ingress 名称"}, exactSecrets)
		rules, _ := content["rule_data"].([]any)
		for index, value := range rules {
			rule, _ := value.(map[string]any)
			addApprovalFields(fields, rule, map[string]string{
				"host_name":    fmt.Sprintf("域名 %d", index+1),
				"context_path": fmt.Sprintf("上下文路径 %d", index+1),
			}, exactSecrets)
		}
	case "6":
		addApprovalFields(fields, content, map[string]string{
			"pod_group_name": "Group 名称",
			"image_version":  "镜像版本",
			"pod_num":        "Pod 数",
			"port":           "容器端口",
		}, exactSecrets)
	case "7", "8":
		addApprovalConfigFiles(fields, content, exactSecrets)
	case "9":
		addApprovalFields(fields, content, map[string]string{"name": "VirtualService 名称"}, exactSecrets)
	}
	return fields
}

func approvalDetailMetadata(row map[string]any, exactSecrets []string) map[string]string {
	fields := make(map[string]string)
	addApprovalFields(fields, row, map[string]string{
		"reason":              "审批备注",
		"message":             "执行消息",
		"external_id":         "工单号",
		"ding_approve_number": "钉钉审批号",
	}, exactSecrets)
	return fields
}

func addApprovalFields(fields map[string]string, source map[string]any, labels map[string]string, exactSecrets []string) {
	for key, label := range labels {
		switch source[key].(type) {
		case string, json.Number, float64, int, int64:
			if value := valueString(source, key); strings.TrimSpace(value) != "" {
				fields[label] = redactApprovalText(value, exactSecrets...)
			}
		}
	}
}

func addApprovalConfigFiles(fields map[string]string, content map[string]any, exactSecrets []string) {
	files, _ := content["encoded_data"].([]any)
	for index, value := range files {
		file, _ := value.(map[string]any)
		body, ok := file["content"].(string)
		if !ok {
			continue
		}
		name, _ := file["name"].(string)
		label := fmt.Sprintf("配置文件 %d", index+1)
		if name != "" {
			label += " (" + redactApprovalText(name, exactSecrets...) + ")"
		}
		fields[label] = redactApprovalConfigFile(name, body, exactSecrets)
	}
}

func redactApprovalConfigFile(name, body string, exactSecrets []string) string {
	if isSensitiveConfigKey(name) {
		return redactedConfigValue
	}
	return redactApprovalText(body, exactSecrets...)
}

func redactApprovalText(value string, exactSecrets ...string) string {
	var structured any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if json.Valid([]byte(value)) && decoder.Decode(&structured) == nil {
		switch structured.(type) {
		case map[string]any, []any:
			var output strings.Builder
			encoder := json.NewEncoder(&output)
			encoder.SetEscapeHTML(false)
			encoder.SetIndent("", "  ")
			if encoder.Encode(redactApprovalJSON(structured, exactSecrets)) == nil {
				return strings.TrimSuffix(output.String(), "\n")
			}
		}
	}
	return redactApprovalPlainText(value, exactSecrets)
}

func redactApprovalJSON(value any, exactSecrets []string) any {
	switch value := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(value))
		namedSecret := isSensitiveConfigKey(valueString(value, "name", "key"))
		for key, child := range value {
			safeKey := RedactExactSecrets(key, exactSecrets...)
			if isSensitiveConfigKey(key) || namedSecret && (key == "value" || key == "content" || key == "data") {
				redacted[safeKey] = redactedConfigValue
			} else {
				redacted[safeKey] = redactApprovalJSON(child, exactSecrets)
			}
		}
		return redacted
	case []any:
		for index, child := range value {
			value[index] = redactApprovalJSON(child, exactSecrets)
		}
		return value
	case string:
		return redactApprovalPlainText(value, exactSecrets)
	case json.Number:
		if RedactExactSecrets(value.String(), exactSecrets...) != value.String() {
			return redactedConfigValue
		}
	}
	return value
}

func redactApprovalPlainText(value string, exactSecrets []string) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return -1
		}
		return character
	}, value)
	if hasAmbiguousApprovalAssignment(strings.Split(value, "\n")) {
		return redactedConfigValue
	}
	document := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "\uFEFF"))
	if strings.HasPrefix(document, "<") {
		if RedactExactSecrets(value, exactSecrets...) != value || hasSensitiveApprovalXML(document, exactSecrets) {
			return redactedConfigValue
		}
		return value
	}
	// A flow mapping can span lines, and a named setting can store its value
	// under a separate key. Line-based redaction cannot safely keep the rest.
	for _, pattern := range []*regexp.Regexp{approvalInlineContainer, approvalNamedSetting} {
		for _, match := range pattern.FindAllStringSubmatch(value, -1) {
			if isSensitiveConfigKey(match[1]) {
				return redactedConfigValue
			}
		}
	}
	return redactApprovalValues(value, exactSecrets)
}

func redactApprovalValues(value string, exactSecrets []string) string {
	value = RedactSensitiveConfig(value, exactSecrets...)
	value = approvalQueryParameter.ReplaceAllStringFunc(value, func(parameter string) string {
		parts := approvalQueryParameter.FindStringSubmatch(parameter)
		name, err := url.QueryUnescape(parts[2])
		if err != nil {
			name = parts[2]
		}
		key := compactConfigKey(name)
		if isSensitiveConfigKey(name) || key == "signature" || key == "xamzsignature" {
			return parts[1] + parts[2] + "=" + redactedConfigValue
		}
		return parameter
	})
	value = approvalBasicAuth.ReplaceAllString(value, `${1}`+redactedConfigValue)
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		lines[index] = approvalInlineAssignment.ReplaceAllStringFunc(line, func(assignment string) string {
			parts := approvalInlineAssignment.FindStringSubmatch(assignment)
			if isSensitiveConfigKey(parts[2]) {
				return parts[1] + redactedConfigValue
			}
			return assignment
		})
	}
	return strings.Join(lines, "\n")
}

func hasAmbiguousApprovalAssignment(lines []string) bool {
	for index, line := range lines {
		assignment := configAssignmentPattern.FindStringSubmatch(line)
		if assignment == nil {
			// Java properties also allow whitespace to separate a key and value.
			if property := approvalSpaceProperty.FindStringSubmatch(line); property != nil && isSensitiveConfigKey(property[1]) {
				return true
			}
			continue
		}
		if !isSensitiveConfigKey(assignment[2]) || index == len(lines)-1 {
			continue
		}

		// A Java properties value continues on the next line after an odd
		// number of trailing backslashes.
		if (len(line)-len(strings.TrimRight(line, "\\")))%2 != 0 {
			return true
		}
		value := strings.TrimSpace(assignment[4])
		blockValue := value == "" || strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">")
		canStartSequence := value == "" || strings.HasPrefix(value, "#")

		// The shared redactor handles the standard indented block forms.
		// Folded values, tagged blocks and indentless sequences also continue.
		for _, nextLine := range lines[index+1:] {
			next := strings.TrimSpace(nextLine)
			if next == "" || strings.HasPrefix(next, "#") {
				continue
			}
			indent := leadingIndent(nextLine)
			keyIndent := leadingIndent(line)
			isSequence := next == "-" || strings.HasPrefix(next, "- ") || strings.HasPrefix(next, "-\t")
			if (indent == keyIndent && isSequence && canStartSequence) || (indent > keyIndent && !blockValue) {
				return true
			}
			break
		}
	}
	return false
}

func hasSensitiveApprovalXML(document string, exactSecrets []string) bool {
	decoder := xml.NewDecoder(strings.NewReader(document))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return false
		}
		if err != nil {
			return true
		}
		text := ""
		switch item := token.(type) {
		case xml.StartElement:
			if isSensitiveConfigKey(item.Name.Local) {
				return true
			}
			for _, attribute := range item.Attr {
				key := strings.ToLower(attribute.Name.Local)
				if key == "key" || key == "name" {
					if isSensitiveConfigKey(attribute.Value) {
						return true
					}
				} else if isSensitiveConfigKey(key) {
					return true
				}
				if redactApprovalValues(attribute.Value, exactSecrets) != attribute.Value {
					return true
				}
			}
		case xml.CharData:
			text = string(item)
		case xml.Comment:
			text = string(item)
		case xml.Directive:
			text = string(item)
		case xml.ProcInst:
			text = string(item.Inst)
		}
		if text != "" && redactApprovalValues(text, exactSecrets) != text {
			return true
		}
	}
}
