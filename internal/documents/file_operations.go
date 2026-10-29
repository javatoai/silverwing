package documents

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type FileOperationKind string

const (
	FileOperationCreateDirectory FileOperationKind = "createDirectory"
	FileOperationCreateFile      FileOperationKind = "createFile"
	FileOperationImport          FileOperationKind = "import"
	FileOperationRename          FileOperationKind = "rename"
)

type FileOperationRequest struct {
	Kind         FileOperationKind `json:"kind"`
	Directory    string            `json:"directory"`
	Name         string            `json:"name"`
	RelativePath string            `json:"relativePath,omitempty"`
	SourcePath   string            `json:"sourcePath,omitempty"`
	Content      []byte            `json:"content,omitempty"`
}

type FileIdentity struct {
	FileKey       string `json:"fileKey,omitempty"`
	Size          int64  `json:"size"`
	ModifiedNanos int64  `json:"modifiedNanos"`
}

type FileOperationConflict struct {
	Request          FileOperationRequest `json:"request"`
	SuggestedName    string               `json:"suggestedName"`
	CanReplace       bool                 `json:"canReplace"`
	ExistingIdentity *FileIdentity        `json:"existingIdentity,omitempty"`
}

type FileConflictChoice string

const (
	FileConflictCancel  FileConflictChoice = "CANCEL"
	FileConflictSaveAs  FileConflictChoice = "SAVE_AS"
	FileConflictReplace FileConflictChoice = "REPLACE"
)

type FileOperationStatus string

const (
	FileOperationCompleted  FileOperationStatus = "COMPLETED"
	FileOperationConflicted FileOperationStatus = "CONFLICT"
	FileOperationCancelled  FileOperationStatus = "CANCELLED"
)

type FileOperationResult struct {
	Status               FileOperationStatus    `json:"status"`
	RelativePath         string                 `json:"relativePath,omitempty"`
	PreviousRelativePath string                 `json:"previousRelativePath,omitempty"`
	IsDirectory          bool                   `json:"isDirectory,omitempty"`
	Conflict             *FileOperationConflict `json:"conflict,omitempty"`
	DraftError           string                 `json:"draftError,omitempty"`
}

type FileOperationBatchResult struct {
	Completed []FileOperationResult  `json:"completed"`
	Failures  []FileOperationFailure `json:"failures,omitempty"`
	Conflict  *FileOperationConflict `json:"conflict,omitempty"`
	Pending   []FileOperationRequest `json:"pending,omitempty"`
}

type FileOperationFailure struct {
	Request FileOperationRequest `json:"request"`
	Message string               `json:"message"`
}

type ClipboardPayload struct {
	Files    []string `json:"files,omitempty"`
	ImagePNG []byte   `json:"imagePng,omitempty"`
}

// ExecuteFileOperation applies one operation under a materials root. Conflicts
// are returned as resumable decisions; no existing file is replaced implicitly.
func (s *Service) ExecuteFileOperation(ctx context.Context, root string, operation FileOperationRequest) (FileOperationResult, error) {
	return s.executeFileOperation(ctx, root, operation, nil)
}

// ExecuteFileOperations runs a selected/imported batch in order and stops at
// the first conflict, retaining the remaining requests for the caller.
func (s *Service) ExecuteFileOperations(ctx context.Context, root string, operations []FileOperationRequest) (FileOperationBatchResult, error) {
	batch := FileOperationBatchResult{Completed: []FileOperationResult{}}
	for index, operation := range operations {
		if err := ctx.Err(); err != nil {
			return batch, err
		}
		result, err := s.ExecuteFileOperation(ctx, root, operation)
		if err != nil {
			if ctx.Err() != nil {
				batch.Pending = append([]FileOperationRequest(nil), operations[index:]...)
				return batch, ctx.Err()
			}
			batch.Failures = append(batch.Failures, FileOperationFailure{Request: operation, Message: err.Error()})
			continue
		}
		if result.Status == FileOperationConflicted {
			batch.Conflict = result.Conflict
			batch.Pending = append([]FileOperationRequest(nil), operations[index+1:]...)
			return batch, nil
		}
		if result.Status == FileOperationCompleted {
			batch.Completed = append(batch.Completed, result)
		}
	}
	return batch, nil
}

func (s *Service) ResolveFileOperationConflict(ctx context.Context, root string, conflict FileOperationConflict, choice FileConflictChoice, alternateName string) (FileOperationResult, error) {
	switch choice {
	case FileConflictCancel:
		return FileOperationResult{Status: FileOperationCancelled}, nil
	case FileConflictSaveAs:
		if strings.TrimSpace(alternateName) == "" {
			alternateName = conflict.SuggestedName
		}
		conflict.Request.Name = alternateName
		return s.ExecuteFileOperation(ctx, root, conflict.Request)
	case FileConflictReplace:
		if !conflict.CanReplace || conflict.ExistingIdentity == nil {
			return FileOperationResult{}, errors.New("文件夹和重命名操作不能替换已有项，请另存或取消")
		}
		return s.executeFileOperation(ctx, root, conflict.Request, &conflict)
	default:
		return FileOperationResult{}, fmt.Errorf("未知文件冲突处理方式：%s", choice)
	}
}

// PasteClipboard prefers native file-list items; image data is only used when
// the file list is empty. Each import is independent, so the batch can resume
// after a conflict without losing later selected files.
func (s *Service) PasteClipboard(ctx context.Context, root, directory string, payload ClipboardPayload) (FileOperationBatchResult, error) {
	files := distinctImportSources(payload.Files)
	operations := make([]FileOperationRequest, 0, len(files))
	for _, source := range files {
		name := filepath.Base(filepath.Clean(source))
		operations = append(operations, FileOperationRequest{Kind: FileOperationImport, Directory: directory, Name: name, SourcePath: source})
	}
	if len(operations) > 0 {
		return s.ExecuteFileOperations(ctx, root, operations)
	}
	if len(payload.ImagePNG) == 0 {
		return FileOperationBatchResult{}, errors.New("剪贴板中没有文件或 PNG 图片，请先复制文件、文件夹或截图再粘贴")
	}
	config, err := png.DecodeConfig(bytes.NewReader(payload.ImagePNG))
	if err != nil {
		return FileOperationBatchResult{}, fmt.Errorf("剪贴板图片不是有效 PNG：%w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		return FileOperationBatchResult{}, errors.New("剪贴板图片尺寸无效或超过 3200 万像素，请缩小图片后粘贴")
	}
	name := "截图-" + time.Now().Local().Format("20060102-150405") + ".png"
	batch, err := s.ExecuteFileOperations(ctx, root, []FileOperationRequest{{Kind: FileOperationCreateFile, Directory: directory, Name: name, Content: payload.ImagePNG}})
	return batch, err
}

// ImportDroppedPaths treats every dropped file or directory as COPY. It shares
// the same staging, conflict, and rollback rules as paste/import operations.
func (s *Service) ImportDroppedPaths(ctx context.Context, root, directory string, paths []string) (FileOperationBatchResult, error) {
	return s.PasteClipboard(ctx, root, directory, ClipboardPayload{Files: paths})
}

func (s *Service) executeFileOperation(ctx context.Context, root string, operation FileOperationRequest, replace *FileOperationConflict) (result FileOperationResult, resultErr error) {
	if err := ctx.Err(); err != nil {
		return FileOperationResult{}, err
	}
	if err := validateName(operation.Name); err != nil {
		return FileOperationResult{}, err
	}
	scope, err := newMaterialsOperationScope(root)
	if err != nil {
		return FileOperationResult{}, err
	}
	publicOperation := operation
	defer func() {
		if !scope.finish(&resultErr) {
			result = FileOperationResult{}
			return
		}
		if result.Conflict != nil {
			result.Conflict.Request = publicOperation
		}
	}()
	rootPath := scope.root
	parent, err := scope.resolve(operation.Directory, false, true, true)
	if err != nil {
		return FileOperationResult{}, err
	}
	if info, err := os.Lstat(parent); err != nil {
		return FileOperationResult{}, err
	} else if !info.IsDir() {
		return FileOperationResult{}, fmt.Errorf("目标不是文件夹：%s", operation.Directory)
	}
	operation.Directory = relativeMaterials(rootPath, parent)
	destination, err := scope.resolve(filepath.Join(scope.public(parent), operation.Name), true, true, false)
	if err != nil {
		return FileOperationResult{}, err
	}
	if operation.Kind == FileOperationRename {
		source, err := scope.resolve(operation.RelativePath, false, true, false)
		if err != nil {
			return FileOperationResult{}, err
		}
		operation.RelativePath = relativeMaterials(rootPath, source)
	}
	var source string
	var sourceInfo os.FileInfo
	switch operation.Kind {
	case FileOperationCreateDirectory, FileOperationCreateFile:
	case FileOperationImport:
		operation.SourcePath, err = scope.importSource(operation.SourcePath)
		if err != nil {
			return FileOperationResult{}, err
		}
		source, sourceInfo, err = validateImportSource(operation.SourcePath)
		if err != nil {
			return FileOperationResult{}, err
		}
		if sourceInfo.IsDir() {
			if pathWithin(source, parent) {
				return FileOperationResult{}, errors.New("不能把文件夹导入到自身目录内")
			}
			if err := validateImportTree(source); err != nil {
				return FileOperationResult{}, err
			}
		}
	case FileOperationRename:
		if operation.RelativePath == "" {
			return FileOperationResult{}, errors.New("重命名路径不能为空")
		}
		source, err = safePath(rootPath, operation.RelativePath, false)
		if err != nil {
			return FileOperationResult{}, err
		}
		sourceInfo, err = os.Lstat(source)
		if err != nil {
			return FileOperationResult{}, err
		}
		if sourceInfo.IsDir() {
			if err := validateMaterialsTree(rootPath, source, false); err != nil {
				return FileOperationResult{}, err
			}
		}
		if filepath.Dir(source) != parent {
			return FileOperationResult{}, errors.New("重命名只能在原文件夹内进行")
		}
	case "":
		return FileOperationResult{}, errors.New("文件操作类型不能为空")
	default:
		return FileOperationResult{}, fmt.Errorf("不支持的文件操作：%s", operation.Kind)
	}

	if operation.Kind == FileOperationRename && samePathName(source, destination) {
		if filepath.Base(source) == operation.Name {
			return completedFileOperation(rootPath, source, operation, sourceInfo.IsDir()), nil
		}
		if err := scope.revalidateDestination(rootPath, operation.Directory, parent); err != nil {
			return FileOperationResult{}, err
		}
		if err := renameCaseOnlyInScope(ctx, rootPath, parent, source, destination, fileIdentity(source, sourceInfo), scope); err != nil {
			return FileOperationResult{}, err
		}
		return completedFileOperation(rootPath, destination, operation, sourceInfo.IsDir()), nil
	}

	existing, err := materialInfo(destination)
	if err != nil {
		return FileOperationResult{}, err
	}
	var expected *FileIdentity
	if existing != nil {
		if err := validateMaterialPath(rootPath, destination, existing); err != nil {
			return FileOperationResult{}, err
		}
		canReplace := existing.Mode().IsRegular() && (operation.Kind == FileOperationCreateFile ||
			(operation.Kind == FileOperationImport && source != destination && sourceInfo.Mode().IsRegular()))
		identity := fileIdentity(destination, existing)
		if replace == nil {
			return FileOperationResult{Status: FileOperationConflicted, Conflict: &FileOperationConflict{
				Request: operation, SuggestedName: availableMaterialsName(parent, operation.Name),
				CanReplace: canReplace, ExistingIdentity: identity,
			}}, nil
		}
		if !canReplace || !replace.CanReplace || replace.ExistingIdentity == nil || !sameFileIdentity(identity, replace.ExistingIdentity) {
			return FileOperationResult{}, errors.New("同名文件在确认期间发生变化，请重新导入并选择处理方式")
		}
		expected = replace.ExistingIdentity
	} else if replace != nil {
		return FileOperationResult{}, errors.New("同名文件在确认期间已被移走，请重新导入")
	}

	if err := scope.revalidateDestination(rootPath, operation.Directory, parent); err != nil {
		return FileOperationResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return FileOperationResult{}, err
	}
	isDirectory := operation.Kind == FileOperationCreateDirectory ||
		((operation.Kind == FileOperationImport || operation.Kind == FileOperationRename) && sourceInfo.IsDir())
	if expected != nil {
		if err := replaceMaterialsFile(ctx, rootPath, parent, destination, operation, source, *expected, scope); err != nil {
			return FileOperationResult{}, err
		}
	} else {
		switch operation.Kind {
		case FileOperationCreateDirectory:
			if err := os.Mkdir(destination, 0o755); err != nil {
				if os.IsExist(err) {
					return FileOperationResult{Status: FileOperationConflicted, Conflict: &FileOperationConflict{Request: operation, SuggestedName: availableMaterialsName(parent, operation.Name)}}, nil
				}
				return FileOperationResult{}, err
			}
		case FileOperationCreateFile:
			if err := writeNewFileStaged(ctx, rootPath, operation.Directory, parent, destination, operation.Content, 0o644, scope); err != nil {
				if os.IsExist(err) {
					return FileOperationResult{Status: FileOperationConflicted, Conflict: &FileOperationConflict{Request: operation, SuggestedName: availableMaterialsName(parent, operation.Name), CanReplace: true, ExistingIdentity: identityForPath(destination)}}, nil
				}
				return FileOperationResult{}, err
			}
		case FileOperationImport:
			if sourceInfo.IsDir() {
				if err := copyDirectoryStaged(ctx, rootPath, parent, source, destination, sourceInfo, scope); err != nil {
					if os.IsExist(err) {
						return FileOperationResult{Status: FileOperationConflicted, Conflict: &FileOperationConflict{Request: operation, SuggestedName: availableMaterialsName(parent, operation.Name)}}, nil
					}
					return FileOperationResult{}, err
				}
			} else if err := copyFileStaged(ctx, rootPath, operation.Directory, parent, source, destination, sourceInfo.Mode().Perm(), scope); err != nil {
				if os.IsExist(err) {
					return FileOperationResult{Status: FileOperationConflicted, Conflict: &FileOperationConflict{Request: operation, SuggestedName: availableMaterialsName(parent, operation.Name), CanReplace: true, ExistingIdentity: identityForPath(destination)}}, nil
				}
				return FileOperationResult{}, err
			}
		case FileOperationRename:
			if err := renameWithRecheck(rootPath, parent, source, destination, fileIdentity(source, sourceInfo), scope); err != nil {
				if os.IsExist(err) {
					return FileOperationResult{Status: FileOperationConflicted, Conflict: &FileOperationConflict{Request: operation, SuggestedName: availableMaterialsName(parent, operation.Name)}}, nil
				}
				return FileOperationResult{}, err
			}
		}
	}
	return completedFileOperation(rootPath, destination, operation, isDirectory), nil
}

func completedFileOperation(root string, path string, operation FileOperationRequest, isDirectory bool) FileOperationResult {
	result := FileOperationResult{Status: FileOperationCompleted, RelativePath: relativeMaterials(root, path), IsDirectory: isDirectory}
	if operation.Kind == FileOperationRename {
		previous := operation.RelativePath
		if !filepath.IsAbs(previous) {
			previous = filepath.Join(root, previous)
		}
		result.PreviousRelativePath = relativeMaterials(root, previous)
	}
	return result
}

func replaceMaterialsFile(ctx context.Context, root, parent, destination string, operation FileOperationRequest, source string, expected FileIdentity, scope *materialsOperationScope) error {
	if err := scope.revalidateDestination(root, relativeMaterials(root, parent), parent); err != nil {
		return err
	}
	staging, err := stagingPath(parent, "import", ".tmp")
	if err != nil {
		return err
	}
	created := false
	defer func() {
		if created {
			_ = os.Remove(staging)
		}
	}()
	if err := createStagedContent(ctx, root, parent, operation, source, staging); err != nil {
		return err
	}
	created = true
	if err := scope.revalidateDestination(root, relativeMaterials(root, parent), parent); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := materialInfo(destination)
	if err != nil {
		return err
	}
	if current == nil || !current.Mode().IsRegular() || !sameFileIdentity(fileIdentity(destination, current), &expected) {
		return errors.New("待替换文件发生了变化，请重新导入")
	}
	if err := validateMaterialPath(root, destination, current); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		return err
	}
	created = false
	return nil
}

func createStagedContent(ctx context.Context, root, parent string, operation FileOperationRequest, source, staging string) error {
	switch operation.Kind {
	case FileOperationCreateFile:
		return writeNewFile(ctx, staging, operation.Content, 0o644)
	case FileOperationImport:
		if info, err := os.Lstat(source); err != nil {
			return err
		} else if info.IsDir() {
			return errors.New("不能用文件替换操作导入文件夹")
		} else {
			return copyFile(ctx, source, staging, info.Mode().Perm())
		}
	default:
		return fmt.Errorf("操作 %s 不支持替换", operation.Kind)
	}
}

func writeNewFileStaged(ctx context.Context, root, directory, parent, destination string, data []byte, mode os.FileMode, scope *materialsOperationScope) error {
	if err := scope.revalidateDestination(root, directory, parent); err != nil {
		return err
	}
	staging, err := stagingPath(parent, "import", ".tmp")
	if err != nil {
		return err
	}
	if err := writeNewFile(ctx, staging, data, mode); err != nil {
		return err
	}
	defer os.Remove(staging)
	if err := scope.revalidateDestination(root, directory, parent); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := materialAbsent(destination); err != nil {
		return err
	}
	return renameNoReplace(staging, destination)
}

func copyFileStaged(ctx context.Context, root, directory, parent, source, destination string, mode os.FileMode, scope *materialsOperationScope) error {
	if err := scope.revalidateDestination(root, directory, parent); err != nil {
		return err
	}
	staging, err := stagingPath(parent, "import", ".tmp")
	if err != nil {
		return err
	}
	if err := copyFile(ctx, source, staging, mode); err != nil {
		return err
	}
	defer os.Remove(staging)
	if err := scope.revalidateDestination(root, directory, parent); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := materialAbsent(destination); err != nil {
		return err
	}
	return renameNoReplace(staging, destination)
}

func copyDirectoryStaged(ctx context.Context, root, parent, source, destination string, sourceInfo os.FileInfo, scope *materialsOperationScope) error {
	if err := scope.revalidateDestination(root, relativeMaterials(root, parent), parent); err != nil {
		return err
	}
	staging, err := stagingPath(parent, "paste", "")
	if err != nil {
		return err
	}
	if err := os.Mkdir(staging, 0o755); err != nil {
		return err
	}
	defer func() { _ = removeOwnedTree(staging) }()
	pending := []directoryCopy{{source: source, target: staging, identity: fileIdentity(source, sourceInfo)}}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		current := pending[0]
		pending = pending[1:]
		currentInfo, err := os.Lstat(current.source)
		if err != nil || validateImportPath(current.source, currentInfo) != nil || !sameFileIdentity(fileIdentity(current.source, currentInfo), current.identity) {
			return errors.New("导入文件夹在复制期间发生变化，请重试")
		}
		children, err := os.ReadDir(current.source)
		if err != nil {
			return err
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			name := child.Name()
			if strings.EqualFold(name, ".git") {
				return errors.New("文件夹包含 Git 内部目录，不能导入")
			}
			if stagingName.MatchString(name) {
				continue
			}
			src := filepath.Join(current.source, name)
			dst := filepath.Join(current.target, name)
			info, err := os.Lstat(src)
			if err != nil {
				return err
			}
			if err := validateImportPath(src, info); err != nil {
				return err
			}
			if info.IsDir() {
				if err := os.Mkdir(dst, 0o755); err != nil {
					return err
				}
				pending = append(pending, directoryCopy{source: src, target: dst, identity: fileIdentity(src, info)})
			} else if err := copyFile(ctx, src, dst, info.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	if err := scope.revalidateDestination(root, relativeMaterials(root, parent), parent); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := materialAbsent(destination); err != nil {
		return err
	}
	return renameNoReplace(staging, destination)
}

type directoryCopy struct {
	source, target string
	identity       *FileIdentity
}

func renameWithRecheck(root, parent, source, destination string, expected *FileIdentity, scope *materialsOperationScope) error {
	if err := scope.revalidateDestination(root, relativeMaterials(root, parent), parent); err != nil {
		return err
	}
	current, err := os.Lstat(source)
	if err != nil || validateMaterialPath(root, source, current) != nil || !sameFileIdentity(fileIdentity(source, current), expected) {
		return errors.New("重命名源文件在操作期间发生变化，请刷新后重试")
	}
	if err := materialAbsent(destination); err != nil {
		return err
	}
	return renameNoReplace(source, destination)
}

func renameCaseOnly(ctx context.Context, root, parent, source, destination string, expected *FileIdentity) error {
	return renameCaseOnlyInScope(ctx, root, parent, source, destination, expected, nil)
}

func renameCaseOnlyInScope(ctx context.Context, root, parent, source, destination string, expected *FileIdentity, scope *materialsOperationScope) error {
	staging, err := stagingPath(parent, "rename", "")
	if err != nil {
		return err
	}
	if err := scope.revalidateDestination(root, relativeMaterials(root, parent), parent); err != nil {
		return err
	}
	current, err := os.Lstat(source)
	if err != nil || validateMaterialPath(root, source, current) != nil || !sameFileIdentity(fileIdentity(source, current), expected) {
		return errors.New("重命名源文件在操作期间发生变化，请刷新后重试")
	}
	if err := renameNoReplace(source, staging); err != nil {
		return err
	}
	staged, stagedErr := os.Lstat(staging)
	if stagedErr != nil || !sameFileIdentity(fileIdentity(staging, staged), expected) {
		if rollback := renameNoReplace(staging, source); rollback != nil {
			return fmt.Errorf("重命名源文件发生变化且回滚失败，文件位于 %s：%w", staging, rollback)
		}
		return errors.New("重命名源文件在操作期间发生变化，请刷新后重试")
	}
	if err := ctx.Err(); err != nil {
		if rollback := renameNoReplace(staging, source); rollback != nil {
			return fmt.Errorf("重命名取消且回滚失败，原文件位于 %s：%w", staging, errors.Join(err, rollback))
		}
		return err
	}
	err = scope.revalidateDestination(root, relativeMaterials(root, parent), parent)
	if err == nil {
		err = renameNoReplace(staging, destination)
	}
	if err != nil {
		if rollback := renameNoReplace(staging, source); rollback != nil {
			return fmt.Errorf("重命名失败且回滚失败，文件位于 %s：%w", staging, errors.Join(err, rollback))
		}
		return err
	}
	return nil
}

func writeNewFile(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	created := true
	defer func() {
		if created {
			_ = os.Remove(path)
		}
	}()
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		_ = file.Close()
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	created = false
	return nil
}

func copyFile(ctx context.Context, source, destination string, mode os.FileMode) error {
	before, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if err := validateImportPath(source, before); err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return errors.New("只能复制普通文件")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return errors.New("导入文件在读取期间发生变化")
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	created := true
	defer func() {
		if created {
			_ = os.Remove(destination)
		}
	}()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			_ = out.Close()
			return err
		}
		count, readErr := in.Read(buffer)
		if count > 0 {
			if _, err := out.Write(buffer[:count]); err != nil {
				_ = out.Close()
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = out.Close()
			return readErr
		}
	}
	afterOpen, statErr := in.Stat()
	afterPath, pathErr := os.Lstat(source)
	if statErr != nil || pathErr != nil || !os.SameFile(before, afterOpen) || !os.SameFile(before, afterPath) ||
		before.Size() != afterOpen.Size() || before.ModTime() != afterOpen.ModTime() {
		_ = out.Close()
		return errors.New("导入文件在复制期间发生变化")
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	created = false
	return nil
}

func stagingPath(parent, kind, suffix string) (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return filepath.Join(parent, ".silverwing-"+kind+"-"+hex.EncodeToString(id[:4])+"-"+hex.EncodeToString(id[4:6])+"-"+
		hex.EncodeToString(id[6:8])+"-"+hex.EncodeToString(id[8:10])+"-"+hex.EncodeToString(id[10:])+suffix), nil
}

func removeOwnedTree(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !sameResolvedPath(path) {
		return os.Remove(path)
	}
	if info.IsDir() {
		children, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := removeOwnedTree(filepath.Join(path, child.Name())); err != nil {
				return err
			}
		}
	}
	return os.Remove(path)
}

func validateImportSource(path string) (string, os.FileInfo, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil, errors.New("导入路径不能为空")
	}
	normalized, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	if hasGitSegment(normalized) {
		return "", nil, errors.New("不能导入 Git 内部文件")
	}
	if err := validateRealAncestors(normalized, true); err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(normalized)
	if err != nil {
		return "", nil, err
	}
	if err := validateImportPath(normalized, info); err != nil {
		return "", nil, err
	}
	return normalized, info, nil
}

func validateImportPath(path string, info os.FileInfo) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsDir() && !info.Mode().IsRegular()) {
		return fmt.Errorf("只能导入普通文件或文件夹：%s", filepath.Base(path))
	}
	if hasGitSegment(path) {
		return errors.New("不能导入 Git 内部文件或文件夹")
	}
	if !sameResolvedPath(path) {
		return fmt.Errorf("导入路径不能包含目录链接或 junction：%s", filepath.Base(path))
	}
	return nil
}

func validateImportTree(root string) error {
	return validateMaterialsTree(root, root, true)
}

func validateMaterialsTree(root, start string, skipStaging bool) error {
	pending := []string{start}
	for len(pending) > 0 {
		path := pending[0]
		pending = pending[1:]
		children, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, child := range children {
			name := child.Name()
			if strings.EqualFold(name, ".git") {
				return errors.New("文件夹包含 Git 内部目录")
			}
			if skipStaging && stagingName.MatchString(name) {
				continue
			}
			entry := filepath.Join(path, name)
			info, err := os.Lstat(entry)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) || !sameResolvedPath(entry) {
				return fmt.Errorf("文件夹包含符号链接、目录链接或特殊文件：%s", name)
			}
			if root != "" && !pathWithin(root, entry) {
				return fmt.Errorf("目录内容越过资料边界：%s", name)
			}
			if info.IsDir() {
				pending = append(pending, entry)
			}
		}
	}
	return nil
}

func resolveMaterialsDirectory(root, relative string) (string, error) {
	if relative == "" || relative == "." {
		return root, nil
	}
	path, err := safePath(root, relative, false)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("目标不是文件夹：%s", relative)
	}
	return path, nil
}

func revalidateMaterialsDestination(root, directory, expectedParent string) error {
	currentRoot, err := validatedMaterialsRoot(root)
	if err != nil {
		return err
	}
	if !samePathName(currentRoot, root) {
		return errors.New("任务资料根目录发生了变化，请刷新后重试")
	}
	currentParent, err := resolveMaterialsDirectory(root, directory)
	if err != nil {
		return err
	}
	if !samePathName(currentParent, expectedParent) {
		return errors.New("任务资料目录发生了变化，请刷新后重试")
	}
	return nil
}

func validatedMaterialsRoot(root string) (string, error) {
	path, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path = filepath.Clean(path)
	if hasGitSegment(path) {
		return "", errors.New("不能在 Git 内部目录管理任务资料")
	}
	if err := validateRealAncestors(path, false); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("任务资料根目录不存在或不是普通目录")
	}
	return path, nil
}

func validateRealAncestors(path string, allowMissingLeaf bool) error {
	path = filepath.Clean(path)
	var chain []string
	for cursor := path; ; cursor = filepath.Dir(cursor) {
		chain = append(chain, cursor)
		if filepath.Dir(cursor) == cursor {
			break
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		cursor := chain[i]
		info, err := os.Lstat(cursor)
		if os.IsNotExist(err) && allowMissingLeaf && i == 0 {
			continue
		}
		if err != nil {
			return fmt.Errorf("文件路径不存在或不可访问：%s: %w", cursor, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("文件路径不能包含符号链接：%s", cursor)
		}
		if !sameResolvedPath(cursor) {
			return fmt.Errorf("文件路径不能包含目录链接或 junction：%s", cursor)
		}
	}
	return nil
}

func validateMaterialPath(root, path string, info os.FileInfo) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return fmt.Errorf("资料路径包含符号链接或特殊文件：%s", filepath.Base(path))
	}
	if !pathWithin(root, path) || !sameResolvedPath(path) {
		return fmt.Errorf("资料路径越界或包含目录链接：%s", filepath.Base(path))
	}
	return nil
}

func materialInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return info, err
}

func fileIdentity(path string, info os.FileInfo) *FileIdentity {
	if info == nil {
		return nil
	}
	return &FileIdentity{FileKey: fileIdentityKey(path), Size: info.Size(), ModifiedNanos: info.ModTime().UnixNano()}
}

func identityForPath(path string) *FileIdentity {
	info, _ := materialInfo(path)
	if info == nil || !info.Mode().IsRegular() {
		return nil
	}
	return fileIdentity(path, info)
}

func sameFileIdentity(a, b *FileIdentity) bool {
	return a != nil && b != nil && a.Size == b.Size && a.ModifiedNanos == b.ModifiedNanos &&
		a.FileKey == b.FileKey
}

func materialAbsent(path string) error {
	_, err := os.Lstat(path)
	if err == nil {
		return os.ErrExist
	}
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func availableMaterialsName(parent, name string) string {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 {
		dot = len(name)
	}
	stem, suffix := name[:dot], name[dot:]
	for index := 1; index <= 10_000; index++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, index, suffix)
		if err := validateName(candidate); err != nil {
			continue
		}
		info, err := materialInfo(filepath.Join(parent, candidate))
		if err == nil && info == nil {
			return candidate
		}
	}
	return name + " (新副本)"
}

func distinctImportSources(paths []string) []string {
	seen := map[string]bool{}
	selected := make([]string, 0, len(paths))
	for _, value := range paths {
		if strings.TrimSpace(value) == "" {
			continue
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			continue
		}
		absolute = filepath.Clean(absolute)
		key := absolute
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			selected = append(selected, absolute)
		}
	}
	return filterMaterialDescendants(selected)
}

func filterMaterialDescendants(paths []string) []string {
	selected := append([]string(nil), paths...)
	return slicesFilter(selected, func(item string) bool {
		for _, parent := range selected {
			if !samePathName(item, parent) && pathWithin(parent, item) {
				info, err := os.Lstat(parent)
				if err == nil && info.IsDir() {
					return false
				}
			}
		}
		return true
	})
}

func slicesFilter(values []string, keep func(string) bool) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if keep(value) {
			result = append(result, value)
		}
	}
	return result
}

func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}

func samePathName(first, second string) bool {
	first, second = filepath.Clean(first), filepath.Clean(second)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(first, second)
	}
	return first == second
}

func sameResolvedPath(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && samePathName(resolved, path)
}

func hasGitSegment(path string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		if strings.EqualFold(segment, ".git") {
			return true
		}
	}
	return false
}

func relativeMaterials(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." {
		return ""
	}
	return filepath.ToSlash(relative)
}
