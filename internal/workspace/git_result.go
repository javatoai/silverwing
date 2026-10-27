package workspace

import (
	"os"

	"strings"
)

func commandFailureMessage(arguments []string, readOnly bool) string {
	verb := "Git 命令失败"
	if readOnly {
		verb = "Git read-only command failed"
	}
	return verb + "：git " + strings.Join(arguments, " ")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
