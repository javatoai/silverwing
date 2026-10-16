package workspace

import "silverwing-go/internal/domain"

const MaxTaskDirectoryNameLength = domain.MaxDirectoryNameLength

// All task creation, previews and configuration identifiers share the Source
// naming rules implemented in domain; separate copies must not drift.
func TaskDirectoryNameError(value string) string {
	if err := domain.DirectoryNameValidationError(value); err != nil {
		return err.Error()
	}
	return ""
}

func RequireValidTaskDirectoryName(value string) (string, error) {
	return domain.RequireValidDirectoryName(value)
}

func SafeDirectoryName(value string) (string, error) {
	return domain.DirectoryName(value)
}
