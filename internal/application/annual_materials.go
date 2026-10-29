package application

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func visibleMaterialsRoot(owned string) string {
	return documents.DescribeMaterials(owned).ReadRoot
}

func annualMaterialsScope(root, subdirectory string) (documents.AnnualMaterialsScope, bool) {
	scope, ok := documents.AnnualScope(filepath.Join(root, subdirectory))
	return scope, ok && scope.Root == filepath.Clean(root)
}

func (a *App) documentAnnualScope(root string) (documents.AnnualMaterialsScope, bool, error) {
	config, err := a.Config.Load()
	if err != nil {
		return documents.AnnualMaterialsScope{}, false, err
	}
	if scope, ok := documents.LocalScope(filepath.Join(root, documents.LocalMaterialsSubdirectory)); ok {
		rel, err := filepath.Rel(value(config.TaskRoot), scope.Base)
		if config.TaskRoot == nil || err != nil || rel == "." || filepath.Dir(rel) != "." || rel == ".." || filepath.IsAbs(rel) {
			return scope, false, errors.New("本地资料所属任务目录已变化，请重新打开任务")
		}
		return scope, true, nil
	}
	scope, ok := annualMaterialsScope(root, value(config.RequirementMaterialsSubdirectory))
	if ok && !sameTaskContextPath(scope.Base, value(config.RequirementMaterialsRoot)) {
		return scope, false, errors.New("资料目录配置已变化，请重新打开任务")
	}
	return scope, ok, nil
}

func materialWriteDenied(path string) error {
	return fmt.Errorf("该资料属于同事或系统管理区域，只允许读取和复制：%s", path)
}

func (a *App) validateMaterialsActionScope(action Action, root string) error {
	manifest, path, err := a.task(action)
	if err != nil {
		return err
	}
	for key, current := range map[string]string{"draftTaskPath": path, "draftMaterialsRoot": root, "desktopRenameTaskPath": path, "desktopRenameMaterialsRoot": root, "materialsHome": a.Paths.Home} {
		if captured := textPayload(action, key); captured != "" && !sameTaskContextPath(captured, current) {
			return errors.New("任务资料目标已变化，旧操作已停止")
		}
	}
	if created := textPayload(action, "desktopRenameTaskCreatedAt"); created != "" && created != manifest.CreatedAt {
		return errors.New("任务身份已变化，旧资料操作已停止")
	}
	if action.Payload["materialsConfigScope"] != nil {
		var expected workspace.MaterialsGitScope
		if err := decodeField(action, "materialsConfigScope", &expected); err != nil {
			return err
		}
		config, err := a.Config.Load()
		if err != nil {
			return err
		}
		if MaterialsGitScope(a.Paths.Home, config) != expected {
			return workspace.ErrMaterialsGitScopeChanged
		}
	}
	return nil
}

// Called inside the existing materials gate and repository-root lock. Copies
// may read peers, but every destination and every destructive source is owned.
func (a *App) authorizeMaterialsAction(root string, action Action) (Action, error) {
	scope, annual, err := a.documentAnnualScope(root)
	if err != nil || !annual {
		return action, err
	}
	check := func(path string, allowRoot bool) error {
		if !scope.Writable(path, allowRoot) {
			return materialWriteDenied(path)
		}
		return nil
	}
	directory := func() string {
		dir := documentDirectory(action, root)
		if dir == "" || dir == "." || filepath.Clean(dir) == scope.Root {
			dir = scope.Owned
		}
		return dir
	}
	path := actionPath(action)
	if path != "" && !scope.Readable(path) {
		return action, errors.New("资料路径超出当前需求或属于系统管理区域")
	}
	if action.Payload == nil {
		action.Payload = map[string]any{}
	} else {
		copy := make(map[string]any, len(action.Payload))
		for k, v := range action.Payload {
			copy[k] = v
		}
		action.Payload = copy
	}
	switch action.Kind {
	case "document.copy-paths", "document.copy-files":
		var paths []string
		paths, err = documentSelectedPaths(action, "paths")
		for _, path := range paths {
			if !scope.Readable(path) {
				err = errors.New("不能读取需求目录之外或系统管理区域")
			}
		}
	case "document.save", "document.draft":
		err = check(path, false)
	case "document.rename":
		if err = check(path, false); err == nil {
			err = check(filepath.Join(filepath.Dir(path), documentEntryName(action)), false)
		}
	case "document.create", "document.mkdir":
		dir, name := fileCreationLocation(root, path, documentEntryName(action))
		if filepath.Clean(dir) == scope.Root || dir == "." {
			dir = scope.Owned
		}
		if err = check(dir, true); err == nil {
			err = check(filepath.Join(dir, name), false)
		}
		action.Path, action.Payload["name"] = dir, name
	case "document.copy":
		target := textPayload(action, "destination")
		if target == "" {
			target = filepath.Join(filepath.Dir(path), documentEntryName(action))
		}
		err = check(target, false)
	case "document.import", "document.drop", "document.paste":
		dir := directory()
		err = check(dir, true)
		if err == nil && action.Kind != "document.paste" {
			var paths []string
			paths, err = documentSelectedPaths(action, "sourcePaths", "paths")
			for _, source := range paths {
				if err == nil {
					err = check(filepath.Join(dir, filepath.Base(source)), false)
				}
			}
		}
		action.Payload["directory"] = dir
	case "document.trash", "document.trash-preview":
		var paths []string
		paths, err = documentSelectedPaths(action, "paths")
		for _, path := range paths {
			if err == nil {
				err = check(path, false)
			}
		}
	case "document.conflict-resolve":
		var conflict documents.FileOperationConflict
		if err = decodeField(action, "conflict", &conflict); err != nil {
			break
		}
		var pending []documents.FileOperationRequest
		if action.Payload["pending"] != nil {
			err = decodeField(action, "pending", &pending)
		}
		requests := append([]documents.FileOperationRequest{conflict.Request}, pending...)
		for _, request := range requests {
			if err != nil {
				break
			}
			if err = check(request.Directory, true); err == nil {
				err = check(filepath.Join(request.Directory, request.Name), false)
			}
			if err == nil && request.Kind == documents.FileOperationRename {
				err = check(request.RelativePath, false)
			}
		}
		if alternate := textPayload(action, "alternateName"); alternate != "" && err == nil {
			err = check(filepath.Join(conflict.Request.Directory, alternate), false)
		}
	}
	return action, err
}

func validAnnualTaskMaterials(manifest domain.TaskManifest, config domain.AppConfig) error {
	owned := value(manifest.RequirementMaterials.WriteRoot)
	if owned == "" {
		return nil
	}
	if scope, ok := documents.LocalScope(owned); ok {
		if config.TaskRoot == nil || manifest.TaskDirectoryName == "" || !sameTaskContextPath(scope.Base, filepath.Join(*config.TaskRoot, manifest.TaskDirectoryName)) {
			return errors.New("本地资料目录与当前任务身份不一致")
		}
		if _, err := scope.DirectoryInstructions(false); err != nil {
			return fmt.Errorf("本地任务资料不可用，可重试准备资料：%w", err)
		}
		return nil
	}
	scope, ok := documents.AnnualScope(owned)
	if !ok {
		return errors.New("此任务使用旧平铺资料目录，请重新准备年度资料目录；原文件保留")
	}
	if value(config.RequirementMaterialsRoot) == "" || value(config.RequirementMaterialsSubdirectory) == "" {
		return errors.New("请先配置任务资料根目录和可写子目录")
	}
	if !sameTaskContextPath(scope.Base, value(config.RequirementMaterialsRoot)) || !strings.EqualFold(filepath.Base(scope.Owned), value(config.RequirementMaterialsSubdirectory)) {
		return errors.New("资料根目录或可写子目录已变化，请重新准备任务资料")
	}
	id := value(manifest.RequirementID)
	if link := domain.ParseFeishuWorkItemLink(manifest.RequirementLink); link != nil {
		id = link.WorkItemID
	}
	if id != "" && !strings.HasPrefix(filepath.Base(scope.Root), id+"-") {
		return errors.New("任务与资料目录的需求身份不一致")
	}
	if info, err := os.Stat(scope.Root); err != nil || !info.IsDir() {
		return errors.New("需求资料目录不存在或不可读取")
	}
	return nil
}
