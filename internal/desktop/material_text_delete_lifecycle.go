package desktop

import (
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/editor"
)

type materialTextDeletePlan struct {
	autosave *editor.Autosave
	keys     []string
	paths    map[string]string
}

func (s *app) captureMaterialTextDelete(action application.Action) (*materialTextDeletePlan, error) {
	if action.Kind != "document.trash" || !dataBool(action.Payload, "confirmed") || s.markdownAutosave == nil {
		return nil, nil
	}
	if err := s.captureMarkdownEditorInput(s.markdown); err != nil {
		return nil, err
	}
	paths, _ := decodeSingleMaterialsData[[]string](action.Payload["paths"])
	if len(paths) == 0 && action.Path != "" {
		paths = []string{action.Path}
	}
	plan := &materialTextDeletePlan{autosave: s.markdownAutosave, paths: make(map[string]string)}
	for key, state := range s.materialTextEditors {
		if state.taskID != action.TaskID {
			continue
		}
		for _, path := range paths {
			if _, inside := pathRelativeToRoot(path, state.path); !inside {
				continue
			}
			if err := s.captureMaterialTextState(state); err != nil {
				return nil, err
			}
			plan.keys = append(plan.keys, key)
			plan.paths[key] = state.path
			break
		}
	}
	s.documentSaveMu.Lock()
	for key, ref := range s.documentSaveRefs {
		if ref.kind == "instruction" || ref.taskID != action.TaskID || plan.paths[key] != "" {
			continue
		}
		for _, path := range paths {
			if _, inside := pathRelativeToRoot(path, ref.path); inside {
				plan.keys = append(plan.keys, key)
				plan.paths[key] = ref.path
				break
			}
		}
	}
	s.documentSaveMu.Unlock()
	if len(plan.keys) == 0 {
		return nil, nil
	}
	return plan, nil
}

func materialTextDeletedPaths(result application.ActionResult) []string {
	items, err := decodeSingleMaterialsData[[]documents.TrashItemResult](result.Data)
	if err != nil {
		return nil
	}
	var paths []string
	for _, item := range items {
		if item.Error == "" && !item.Cancelled {
			paths = append(paths, item.Path)
		}
	}
	return paths
}

func materialTextDeleteContains(paths []string, path string) bool {
	for _, removed := range paths {
		if _, inside := pathRelativeToRoot(removed, path); inside {
			return true
		}
	}
	return false
}

func (request *materialsFileJob) finishMaterialTextDeleteLeaseLocked(result application.ActionResult) {
	if request.textDeleteLease == nil || request.textDelete == nil {
		return
	}
	paths := materialTextDeletedPaths(result)
	for key, path := range request.textDelete.paths {
		if materialTextDeleteContains(paths, path) {
			request.textDelete.autosave.AcceptDisk(key, "", true)
		}
	}
	request.textDeleteLease.Release()
	request.textDeleteLease = nil
}

func (request *materialsFileJob) finishMaterialTextDeleteLease(result application.ActionResult) {
	request.leaseMu.Lock()
	defer request.leaseMu.Unlock()
	request.finishMaterialTextDeleteLeaseLocked(result)
}

// Retire only captured identities. A later file with the same path has its own
// native owner and must not be closed by the old delete's completion.
func (s *app) retireMaterialTextDeleteResult(request *materialsFileJob, result application.ActionResult) {
	if request.textDelete == nil {
		return
	}
	paths := materialTextDeletedPaths(result)
	for key, path := range request.textDelete.paths {
		if !materialTextDeleteContains(paths, path) {
			continue
		}
		state := s.materialTextEditors[key]
		if state != nil && state.path != path {
			continue
		}
		if state != nil {
			state.closed = true
		}
		s.documentSaveMu.Lock()
		if ref, exists := s.documentSaveRefs[key]; exists && ref.path == path && ref.textOwner == state {
			delete(s.documentSaveRefs, key)
		}
		s.documentSaveMu.Unlock()
		delete(s.materialTextEditors, key)
		delete(s.documentDrafts, path)
		delete(s.documentDirtyMap, path)
		if state != nil && s.materialTextActive == state {
			s.materialTextActive = nil
		}
	}
}
