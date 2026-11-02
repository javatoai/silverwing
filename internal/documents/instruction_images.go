package documents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InstructionImageResource is relative to the instruction document, rather
// than a client-selected materials root. The UI owns Markdown escaping.
type InstructionImageResource struct {
	RelativePath string `json:"relativePath"`
	Text         string `json:"text"`
}

type InstructionImageImportResult struct {
	Resources []InstructionImageResource `json:"resources"`
}

// ImportInstructionImages copies images next to a server-resolved document.
// It never writes the document body: callers insert references only after the
// entire import succeeds and their editor identity is still current.
func (s *Service) ImportInstructionImages(ctx context.Context, root, documentPath string, payload ClipboardPayload) (result InstructionImageImportResult, resultErr error) {
	if err := ctx.Err(); err != nil {
		return InstructionImageImportResult{}, err
	}
	root, documentPath, err := taskRulesTarget(root, documentPath)
	if err != nil {
		return InstructionImageImportResult{}, err
	}
	if info, err := os.Lstat(documentPath); err == nil {
		if !info.Mode().IsRegular() {
			return InstructionImageImportResult{}, errors.New("说明文档不是普通文件")
		}
	} else if !os.IsNotExist(err) {
		return InstructionImageImportResult{}, err
	}
	files, err := instructionImageSources(payload.Files)
	if err != nil {
		return InstructionImageImportResult{}, err
	}
	if len(files) == 0 && len(payload.ImagePNG) == 0 {
		return InstructionImageImportResult{Resources: []InstructionImageResource{}}, nil
	}

	// Reuse the document service's real-directory checks and keep the selected
	// directory identities alive across the batch. A changed scope cannot return
	// references even if an earlier copy completed.
	var directories []materialsDirectoryIdentity
	defer func() {
		for i := range directories {
			resultErr = errors.Join(resultErr, directories[i].check())
			directories[i].close()
		}
		if resultErr != nil {
			result = InstructionImageImportResult{}
		}
	}()
	pin := func(path string) error {
		identity, err := captureMaterialsDirectory(path, false)
		if err == nil {
			directories = append(directories, identity)
		}
		return err
	}
	if err := pin(root); err != nil {
		return InstructionImageImportResult{}, err
	}
	parent := filepath.Dir(documentPath)
	relativeParent, err := filepath.Rel(root, parent)
	if err != nil {
		return InstructionImageImportResult{}, err
	}
	current := root
	if relativeParent != "." {
		for _, segment := range strings.Split(relativeParent, string(filepath.Separator)) {
			if err := ctx.Err(); err != nil {
				return InstructionImageImportResult{}, err
			}
			current = filepath.Join(current, segment)
			if err := ensureMaterialsDirectory(root, current); err != nil {
				return InstructionImageImportResult{}, err
			}
			if err := pin(current); err != nil {
				return InstructionImageImportResult{}, err
			}
		}
	}
	assetsName := strings.TrimSuffix(filepath.Base(documentPath), filepath.Ext(documentPath)) + ".assets"
	assets := filepath.Join(parent, assetsName)
	created, err := s.ExecuteFileOperation(ctx, root, FileOperationRequest{Kind: FileOperationCreateDirectory, Directory: parent, Name: assetsName})
	if err != nil {
		return InstructionImageImportResult{}, err
	}
	if created.Status != FileOperationCompleted && created.Status != FileOperationConflicted {
		return InstructionImageImportResult{}, errors.New("未能创建说明图片目录")
	}
	if err := pin(assets); err != nil {
		return InstructionImageImportResult{}, err
	}

	inputs := make([]ClipboardPayload, 0, len(files)+1)
	for _, source := range files {
		inputs = append(inputs, ClipboardPayload{Files: []string{source}})
	}
	if len(files) == 0 {
		inputs = append(inputs, ClipboardPayload{ImagePNG: payload.ImagePNG})
	}
	result.Resources = make([]InstructionImageResource, 0, len(inputs))
	for _, input := range inputs {
		batch, err := s.PasteClipboard(ctx, root, assets, input)
		if err != nil {
			return InstructionImageImportResult{}, err
		}
		if len(batch.Failures) != 0 {
			return InstructionImageImportResult{}, fmt.Errorf("图片导入失败：%s", batch.Failures[0].Message)
		}
		for batch.Conflict != nil {
			resolved, err := s.ResolveFileOperationConflict(ctx, root, *batch.Conflict, FileConflictSaveAs, "")
			if err != nil {
				return InstructionImageImportResult{}, err
			}
			batch.Conflict = resolved.Conflict
			if resolved.Status == FileOperationCompleted {
				batch.Completed = append(batch.Completed, resolved)
			} else if resolved.Status != FileOperationConflicted || resolved.Conflict == nil {
				return InstructionImageImportResult{}, errors.New("图片导入已取消")
			}
		}
		if len(batch.Pending) != 0 || len(batch.Completed) != 1 || batch.Completed[0].IsDirectory {
			return InstructionImageImportResult{}, errors.New("部分图片未能导入")
		}
		copied := filepath.Join(root, filepath.FromSlash(batch.Completed[0].RelativePath))
		if !samePathName(filepath.Dir(copied), assets) || !instructionImageExtension(copied) {
			return InstructionImageImportResult{}, errors.New("图片导入结果超出说明图片目录")
		}
		relative, err := filepath.Rel(parent, copied)
		if err != nil {
			return InstructionImageImportResult{}, err
		}
		name := filepath.Base(copied)
		result.Resources = append(result.Resources, InstructionImageResource{RelativePath: filepath.ToSlash(relative), Text: strings.TrimSuffix(name, filepath.Ext(name))})
	}
	if err := ctx.Err(); err != nil {
		return InstructionImageImportResult{}, err
	}
	return result, nil
}

func instructionImageExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func instructionImageSources(paths []string) ([]string, error) {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if !instructionImageExtension(path) {
			return nil, fmt.Errorf("只支持 PNG、JPG、JPEG、GIF 和 WebP 图片：%s", filepath.Base(path))
		}
		_, info, err := validateImportSource(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("只能导入普通图片文件")
		}
	}
	return distinctImportSources(paths), nil
}
