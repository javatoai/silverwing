package desktop

import (
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
)

// applyMaterialsActionResult applies only materials feature results on the UI thread.
func (s *app) applyMaterialsActionResult(action application.Action, result application.ActionResult) bool {
	if s.applyMaterialsFileActionResult(action, result) {
		return true
	}
	switch action.Kind {
	case "document.list":
		if action.TaskID != s.taskUnit.currentTask().id {
			return true
		}
		s.materialsUnit.state.documentsRequested = false
		s.materialsUnit.state.documentsLoaded = true
		entries, err := decodeData[application.Document](result.Data)
		if err != nil {
			s.lastError = "读取资料列表格式失败：" + err.Error()
			return true
		}
		s.materialsUnit.replaceDocuments(entries)
		s.reconcileMaterialsDocuments(entries)
		state := s.materialsUnit.state.materialsFileManagement
		pendingSelection := state != nil && (state.pendingSelectPath != "" || state.pendingSelectDir != "")
		if pendingSelection {
			s.applyPendingMaterialsFileSelection()
		} else {
			if !s.restoreReadingMaterials() && s.materialsUnit.state.documentPath == "" {
				if first := firstDocument(entries); first != "" {
					s.selectDocument(first)
				}
			}
		}
	case "document.read":
		s.observeMaterialTextInput()
		if action.TaskID != s.taskUnit.currentTask().id {
			return true
		}
		opening := s.materialsUnit.state.documentReadPending
		s.materialsUnit.state.documentReadPending = false
		path := result.Path
		if path == "" {
			path = action.Path
		}
		if path == s.materialsUnit.state.documentPath {
			s.materialsUnit.state.documentKind = dataString(result.Data, "kind")
			if s.materialsUnit.state.documentKind == "" {
				s.materialsUnit.state.documentKind = documentKindForPath(path)
			}
			s.materialsUnit.state.documentReadOnly = dataBool(result.Data, "readOnly") || markdownReadingOnly && s.materialsUnit.state.documentKind == "markdown"
			if opening || s.materialsUnit.state.documentKind != "markdown" || s.materialsUnit.state.documentReadOnly {
				s.materialsUnit.state.documentEditing = false
			}
			diskContent := result.Text
			localContent := diskContent
			if draft, ok := s.materialsUnit.state.documentDrafts[path]; ok && !s.materialsUnit.state.documentReadOnly {
				s.materialsUnit.state.documentContent = draft
				s.materialsUnit.state.documentDirty = true
				localContent = draft
			} else {
				s.materialsUnit.state.documentContent = diskContent
				s.materialsUnit.state.documentDirty = false
			}
			s.materialsUnit.state.documentHash = dataString(result.Data, "hash")
			if s.materialsUnit.state.documentKind == "markdown" {
				content, err := decodeSingleMaterialsData[documents.Content](result.Data)
				if err != nil {
					s.lastError = "读取 Markdown 草稿格式失败：" + err.Error()
				}
				s.seedMarkdownAutosaveDraft(action.TaskID, path, diskContent, localContent, s.materialsUnit.state.documentHash, content.Draft, content.DraftError, content)
			}
			if documents.EditableMaterialText(path) && !s.materialsUnit.state.documentReadOnly {
				content, err := decodeSingleMaterialsData[documents.Content](result.Data)
				if err != nil {
					s.lastError = "读取文本草稿格式失败：" + err.Error()
				} else {
					s.seedMaterialTextEditor(action.TaskID, path, content)
				}
			}
			if s.materialsUnit.state.documentKind == "image" || s.materialsUnit.state.documentKind == "pdf" {
				s.materialsUnit.state.documentContent = ""
				s.openNativeMedia(result.Path, s.materialsUnit.state.documentKind)
			} else {
				s.materialsUnit.closeNativeMedia()
				s.syncMarkdownDocument(path, s.materialsUnit.state.documentContent)
			}
			s.applyPendingMaterialsSearchHit(action, result)
		}
		s.applyMaterialsFileReadResult(action)
	case "document.save":
		if markdownReadingOnly && documentKindForPath(action.Path) == "markdown" {
			return true
		}
		hash := dataString(result.Data, "hash")
		if hash == "" {
			s.lastError = "保存请求已返回，但后端没有提供新版本标识；请重新读取文件确认"
			return true
		}
		if key := s.documentAutosaveKeyForTask(action.TaskID, action.Path); key != "" && s.materialsUnit.state.markdownAutosave != nil && documentKindForPath(action.Path) == "markdown" {
			if ref, ok := s.materialsUnit.documentSaveRef(key); ok {
				s.materialsUnit.state.documentSaveMu.Lock()
				ref.hash, ref.overwrite = hash, false
				s.materialsUnit.state.documentSaveRefs[key] = ref
				s.materialsUnit.state.documentSaveMu.Unlock()
				s.materialsUnit.state.markdownAutosave.AcceptDisk(key, action.Text, true)
			}
		}
		if action.TaskID != s.taskUnit.currentTask().id || action.Path != s.materialsUnit.state.documentPath {
			return true
		}
		s.materialsUnit.state.documentHash = hash
		s.materialsUnit.state.documentDirty = false
		delete(s.materialsUnit.state.documentDrafts, action.Path)
		delete(s.materialsUnit.state.documentDirtyMap, action.Path)
		s.materialsUnit.state.documentsRequested = false
		s.loadDocuments()
	case "document.rename":
		s.applyMaterialsRenameResult(action, result)
	default:
		return false
	}
	return true
}
