package documents

// MoveDirectoryNoReplace publishes a directory at an unoccupied path on the
// same filesystem. Callers validate and own both parent directories.
func MoveDirectoryNoReplace(source, destination string) error {
	if _, err := DirectoryFingerprint(source); err != nil {
		return err
	}
	return renameNoReplace(source, destination)
}
