package desktop

import "github.com/egoist/mygo/ui"

// Reserve space for the title, count and the existing sort/more/pane controls.
const materialsTreeInlineActionsMinWidth = float32(320)

func (s *app) materialsTreeHeaderActions(c *ui.Context, p palette, canWrite bool) {
	ui.Row(c).Gap(0).Shrink(0).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Disabled(!canWrite || s.materialsFileInputBusy()).Children(func() {
			if fileTreeIconButton(c, p, "materials-header-new-file", "file-plus", "新建文档").Clicked() {
				s.openMaterialsNewEntry("document.create", "新建文档")
			}
			if fileTreeIconButton(c, p, "materials-header-new-folder", "folder-plus", "新建文件夹").Clicked() {
				s.openMaterialsNewEntry("document.mkdir", "新建文件夹")
			}
		})
		if fileTreeIconButton(c, p, "materials-header-refresh", "refresh", "刷新资料").Clicked() {
			s.reloadMaterialsDocuments()
		}
		if fileTreeIconButton(c, p, "materials-header-collapse", "collapse-folders", "收起全部文件夹").Clicked() {
			s.collapseMaterialsTreeDirectories()
		}
	})
}

func (s *app) openMaterialsNewEntry(kind, title string) {
	directory, _ := s.materialsCurrentDirectory(s.ensureMaterialsFileManagementState())
	s.openMaterialsFileNameForm(kind, title, directory)
}

func (s *app) collapseMaterialsTreeDirectories() {
	for directory := range s.readingExpandedDirectories() {
		s.setReadingDirectoryExpanded(directory, false)
	}
}
