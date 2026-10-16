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
		if action.TaskID != s.currentTask().id {
			return true
		}
		s.documentsRequested = false
		s.documentsLoaded = true
		entries, err := decodeData[application.Document](result.Data)
		if err != nil {
			s.lastError = "读取资料列表格式失败：" + err.Error()
			return true
		}
		s.documents = entries
		s.reconcileMaterialsDocuments(entries)
		state := s.materialsFileManagement
		pendingSelection := state != nil && (state.pendingSelectPath != "" || state.pendingSelectDir != "")
		if pendingSelection {
			s.applyPendingMaterialsFileSelection()
		} else {
			if !s.restoreReadingMaterials() && s.documentPath == "" {
				if first := firstDocument(entries); first != "" {
					s.selectDocument(first)
				}
			}
		}
	case "document.read":
		s.observeMaterialTextInput()
		if action.TaskID != s.currentTask().id {
			return true
		}
		opening := s.documentReadPending
		s.documentReadPending = false
		path := result.Path
		if path == "" {
			path = action.Path
		}
		if path == s.documentPath {
			s.documentKind = dataString(result.Data, "kind")
			if s.documentKind == "" {
				s.documentKind = documentKindForPath(path)
			}
			s.documentReadOnly = dataBool(result.Data, "readOnly") || markdownReadingOnly && s.documentKind == "markdown"
			if opening || s.documentKind != "markdown" || s.documentReadOnly {
				s.documentEditing = false
			}
			diskContent := result.Text
			localContent := diskContent
			if draft, ok := s.documentDrafts[path]; ok && !s.documentReadOnly {
				s.documentContent = draft
				s.documentDirty = true
				localContent = draft
			} else {
				s.documentContent = diskContent
				s.documentDirty = false
			}
			s.documentHash = dataString(result.Data, "hash")
			if s.documentKind == "markdown" {
				content, err := decodeSingleMaterialsData[documents.Content](result.Data)
				if err != nil {
					s.lastError = "读取 Markdown 草稿格式失败：" + err.Error()
				}
				s.seedMarkdownAutosaveDraft(action.TaskID, path, diskContent, localContent, s.documentHash, content.Draft, content.DraftError, content)
			}
			if documents.EditableMaterialText(path) && !s.documentReadOnly {
				content, err := decodeSingleMaterialsData[documents.Content](result.Data)
				if err != nil {
					s.lastError = "读取文本草稿格式失败：" + err.Error()
				} else {
					s.seedMaterialTextEditor(action.TaskID, path, content)
				}
			}
			if s.documentKind == "image" || s.documentKind == "pdf" {
				s.documentContent = ""
				s.openNativeMedia(result.Path, s.documentKind)
			} else {
				s.closeNativeMedia()
				s.syncMarkdownDocument(path, s.documentContent)
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
		if key := s.documentAutosaveKeyForTask(action.TaskID, action.Path); key != "" && s.markdownAutosave != nil && documentKindForPath(action.Path) == "markdown" {
			if ref, ok := s.documentSaveRef(key); ok {
				s.documentSaveMu.Lock()
				ref.hash, ref.overwrite = hash, false
				s.documentSaveRefs[key] = ref
				s.documentSaveMu.Unlock()
				s.markdownAutosave.AcceptDisk(key, action.Text, true)
			}
		}
		if action.TaskID != s.currentTask().id || action.Path != s.documentPath {
			return true
		}
		s.documentHash = hash
		s.documentDirty = false
		delete(s.documentDrafts, action.Path)
		delete(s.documentDirtyMap, action.Path)
		s.documentsRequested = false
		s.loadDocuments()
	case "document.rename":
		s.applyMaterialsRenameResult(action, result)
	default:
		return false
	}
	return true
}
