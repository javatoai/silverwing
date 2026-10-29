package desktop

// The zero-value settings controller composes the same safe adapter as the
// desktop shell. A supplied port remains authoritative for every new preview.
func (c *settingsController) previewResourceResolver() localPreviewFileResolver {
	if c.previewFiles == nil {
		c.previewFiles = newDocumentLocalPreviewFileResolver()
	}
	return c.previewFiles
}
