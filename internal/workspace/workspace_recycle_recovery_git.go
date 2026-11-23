package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

type workspaceRecycleMetadata struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory,omitempty"`
	Mode      uint32 `json:"mode"`
	Digest    string `json:"digest,omitempty"`
}

type workspaceRecyclePack struct {
	Hash    string   `json:"hash"`
	Digest  string   `json:"digest"`
	Objects []string `json:"objects"`
}

func workspaceRecycleGit(git *GitClient) *GitClient {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	local := *git
	local.RemoveEnvironment = gitCompareEnvironmentRemovals(git.RemoveEnvironment)
	local.Environment = make(map[string]string, len(git.Environment)+3)
	for name, value := range git.Environment {
		if !slices.ContainsFunc(local.RemoveEnvironment, func(removed string) bool { return strings.EqualFold(name, removed) }) {
			local.Environment[name] = value
		}
	}
	local.Environment["GIT_NO_REPLACE_OBJECTS"] = "1"
	local.Environment["GIT_NO_LAZY_FETCH"] = "1"
	local.Environment["GIT_TERMINAL_PROMPT"] = "0"
	return &local
}

func workspaceRecycleDigest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func syncRecycleDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func openRecycleRegular(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	reparse, err := bootstrapPathIsReparsePoint(path, info)
	if err != nil {
		return nil, err
	}
	if reparse || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("工作区回收元数据不是安全的普通文件：%s", path)
	}
	file, err := openCloneRegularFile(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, fmt.Errorf("工作区回收元数据在读取时发生变化：%s", path)
	}
	return file, nil
}

func checkRecycleOptionalRegular(path string) error {
	file, err := openRecycleRegular(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return file.Close()
}

func readRecycleRegular(path string, maximum int64) ([]byte, error) {
	file, err := openRecycleRegular(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("工作区回收元数据超过允许大小：%s", path)
	}
	return data, nil
}

func (r *workspaceRecycleRecord) readLinkedPointer() ([]byte, error) {
	if !filepath.IsAbs(r.CommonDirectory) || !sameRecyclePath(filepath.Dir(r.AdminDirectory), filepath.Join(r.CommonDirectory, "worktrees")) {
		return nil, fmt.Errorf("回收记录的 worktree 登记路径不安全：%s", r.AdminDirectory)
	}
	data, err := readRecycleRegular(filepath.Join(r.Target, ".git"), 64*1024)
	if err != nil {
		return nil, err
	}
	key, value, ok := strings.Cut(strings.TrimSpace(string(data)), ":")
	if !ok || key != "gitdir" || strings.TrimSpace(value) == "" || !sameRecyclePath(resolveGitFilePath(r.Target, strings.TrimSpace(value)), r.AdminDirectory) {
		return nil, fmt.Errorf("工作区 .git 指针与原登记位置不匹配：%s", r.Target)
	}
	if r.GitFileDigest != "" && workspaceRecycleDigest(data) != r.GitFileDigest {
		return nil, fmt.Errorf("工作区 .git 指针已变化：%s", r.Target)
	}
	return data, nil
}

func (r *workspaceRecycleRecord) verifyAdminPointers(directory string) error {
	for _, expected := range []struct{ file, raw, path string }{
		{"HEAD", "ref: refs/heads/" + r.Branch, ""},
		{"gitdir", r.AdminGitDirFile, filepath.Join(r.Target, ".git")},
		{"commondir", "", r.CommonDirectory},
	} {
		data, err := readRecycleRegular(filepath.Join(directory, expected.file), 64*1024)
		if err != nil {
			return err
		}
		value := strings.TrimSpace(string(data))
		if value == "" || (expected.raw != "" && value != expected.raw) ||
			(expected.path != "" && !sameRecyclePath(resolveGitFilePath(r.AdminDirectory, value), expected.path)) {
			return fmt.Errorf("原 Git 登记内容与回收记录不匹配：%s", filepath.Join(r.AdminDirectory, expected.file))
		}
	}
	return nil
}

func (r *workspaceRecycleRecord) verifyLive(ctx context.Context, git *GitClient) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.checkTargetIdentity(); err != nil {
		return err
	}
	if err := checkRecycleDirectory(r.CommonDirectory, r.CommonIdentity); err != nil {
		return err
	}
	if r.Workspace.Strategy == domain.WorkspaceStandardWorktree {
		if _, err := r.readLinkedPointer(); err != nil {
			return err
		}
		if err := r.verifyOriginalAdmin(ctx); err != nil {
			return err
		}
	} else {
		owner, err := verifyModuleRecycleClone(ctx, git, r.workspaceDirectory(), r.Workspace, r.Target)
		if err != nil {
			return err
		}
		if owner != r.CloneOwner {
			return fmt.Errorf("独立克隆所有权标记已变化：%s", r.Target)
		}
	}
	operation, err := gitOperationInProgressContext(ctx, git, r.Target)
	if err != nil {
		return err
	}
	if operation != "" {
		return fmt.Errorf("工作区有进行中的 Git 操作 %s：%s", operation, r.Target)
	}
	branch, head, status, err := workspaceStateFingerprint(ctx, git, r.Target)
	if err != nil {
		return err
	}
	if branch != r.Branch || head != r.Head || status != r.statusOutput {
		return fmt.Errorf("工作区 Git 状态已变化，请重新确认：%s", r.Target)
	}
	return nil
}

func (r *workspaceRecycleRecord) verifyOriginalAdmin(ctx context.Context) error {
	if err := checkRecycleDirectory(r.RepositoryRoot, r.RepositoryIdentity); err != nil {
		return err
	}
	if err := checkRecycleDirectory(r.AdminDirectory, r.AdminIdentity); err != nil {
		return err
	}
	return r.verifyAdminSnapshot(ctx, r.AdminDirectory, true)
}

func (r *workspaceRecycleRecord) verifyAdminSnapshot(ctx context.Context, directory string, checkMode bool) error {
	if err := r.verifyAdminPointers(directory); err != nil {
		return err
	}
	entries, err := collectRecycleAdmin(ctx, directory, "")
	if err != nil {
		return err
	}
	if !slices.EqualFunc(r.AdminFiles, entries, func(saved, live workspaceRecycleMetadata) bool {
		return saved.Path == live.Path && saved.Directory == live.Directory && saved.Digest == live.Digest && (!checkMode || saved.Mode == live.Mode)
	}) {
		return fmt.Errorf("Git 登记目录或索引已变化：%s", directory)
	}
	return nil
}

func (r *workspaceRecycleRecord) verifyBackup(ctx context.Context) error {
	if err := r.verifyReceipt(); err != nil {
		return err
	}
	if r.Workspace.Strategy != domain.WorkspaceStandardWorktree {
		return nil
	}
	if err := r.verifyAdminSnapshot(ctx, filepath.Join(r.directory, workspaceRecycleAdmin), false); err != nil {
		return err
	}
	if r.IndexPack == nil {
		return nil
	}
	digest, err := copyRecycleFile(ctx, r.packPath(), "", 0)
	if err != nil {
		return err
	}
	if digest != r.IndexPack.Digest {
		return errors.New("已保存的 Git 暂存对象包已变化")
	}
	return nil
}

func (r *workspaceRecycleRecord) captureLinkedMetadata(ctx context.Context, git *GitClient) error {
	if err := r.verifyAdminPointers(r.AdminDirectory); err != nil {
		return err
	}
	backup := filepath.Join(r.directory, workspaceRecycleAdmin)
	if err := os.Mkdir(backup, 0o700); err != nil {
		return err
	}
	entries, err := collectRecycleAdmin(ctx, r.AdminDirectory, backup)
	if err != nil {
		return err
	}
	r.AdminFiles = entries
	for _, entry := range slices.Backward(entries) {
		if entry.Directory {
			if err := syncRecycleDirectory(filepath.Join(backup, filepath.FromSlash(entry.Path))); err != nil {
				return err
			}
		}
	}
	if err := syncRecycleDirectory(backup); err != nil {
		return err
	}
	shared, err := git.RunContext(ctx, r.Target, 2*time.Minute, true, true, "rev-parse", "--path-format=absolute", "--shared-index-path")
	if err != nil {
		return err
	}
	if sharedPath := strings.TrimSpace(shared.Stdout); sharedPath != "" &&
		!sameRecyclePath(filepath.Dir(resolveGitFilePath(r.Target, sharedPath)), r.AdminDirectory) {
		return fmt.Errorf("分离索引位于原 Git 登记目录之外：%s", sharedPath)
	}
	return r.captureIndexPack(ctx, git)
}

// copyRecycleFile hashes file bytes while optionally copying them into a new,
// exclusive destination. Git indexes and packs can be large.
func copyRecycleFile(ctx context.Context, source, target string, mode os.FileMode) (string, error) {
	input, err := openRecycleRegular(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	sourceBytes := recycleFileReader{ctx: ctx, Reader: input}
	hash := sha256.New()
	if target == "" {
		_, err = io.Copy(hash, sourceBytes)
		return fmt.Sprintf("%x", hash.Sum(nil)), err
	}
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return "", err
	}
	if _, err = io.Copy(io.MultiWriter(hash, output), sourceBytes); err == nil {
		err = output.Chmod(mode)
	}
	if err == nil {
		err = output.Sync()
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), errors.Join(err, output.Close())
}

type recycleFileReader struct {
	ctx context.Context
	io.Reader
}

func (r recycleFileReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func collectRecycleAdmin(ctx context.Context, root, backup string) ([]workspaceRecycleMetadata, error) {
	if _, err := documents.DirectoryFingerprint(root); err != nil {
		return nil, err
	}
	var entries []workspaceRecycleMetadata
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil || path == root {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		reparse, err := bootstrapPathIsReparsePoint(path, info)
		if err != nil {
			return err
		}
		if reparse || (!entry.IsDir() && !info.Mode().IsRegular()) || strings.HasSuffix(strings.ToLower(entry.Name()), ".lock") ||
			(entry.Name() == "locked" && filepath.Dir(path) == root) {
			return fmt.Errorf("原 Git 登记含有链接、锁或不安全文件：%s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		node := workspaceRecycleMetadata{Path: filepath.ToSlash(relative), Directory: entry.IsDir(), Mode: uint32(info.Mode().Perm())}
		destination := ""
		if backup != "" {
			destination = filepath.Join(backup, relative)
		}
		if node.Directory {
			if backup != "" {
				if err := os.Mkdir(destination, 0o700); err != nil {
					return err
				}
			}
		} else {
			node.Digest, err = copyRecycleFile(ctx, path, destination, 0o600)
			if err != nil {
				return err
			}
		}
		entries = append(entries, node)
		return nil
	})
	return entries, err
}

func (r *workspaceRecycleRecord) packPath() string {
	return filepath.Join(r.directory, "index-"+r.IndexPack.Hash+".pack")
}

func (r *workspaceRecycleRecord) captureIndexPack(ctx context.Context, git *GitClient) error {
	index, err := git.RunContext(ctx, r.Target, 2*time.Minute, true, true, "ls-files", "--stage", "--resolve-undo", "-z")
	if err != nil {
		return err
	}
	objects := make(map[string]bool)
	for entry := range strings.SplitSeq(index.Stdout, "\x00") {
		if entry == "" {
			continue
		}
		metadata, _, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(metadata)
		if !ok || len(fields) != 3 || !validGitObjectID(fields[1]) || len(fields[1]) != len(r.Head) {
			return errors.New("Git 索引记录格式无法验证")
		}
		if fields[0] != "160000" && !zeroGitObjectID(fields[1]) {
			objects[fields[1]] = true
		}
	}
	if len(objects) == 0 {
		return nil
	}
	committed, err := git.RunContext(ctx, r.Target, 2*time.Minute, true, true, "ls-tree", "-r", "-z", "--full-tree", r.Head)
	if err != nil {
		return err
	}
	for entry := range strings.SplitSeq(committed.Stdout, "\x00") {
		if entry == "" {
			continue
		}
		metadata, _, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(metadata)
		if !ok || len(fields) != 3 || !validGitObjectID(fields[2]) {
			return errors.New("Git 已提交文件记录格式无法验证")
		}
		delete(objects, fields[2])
	}
	if len(objects) == 0 {
		return nil
	}
	if _, err := r.indexPackDestination(ctx, git); err != nil {
		return err
	}
	ids := slices.Sorted(maps.Keys(objects))
	args := []string{"--no-optional-locks", "-c", "core.longpaths=true", "-c", "pack.packSizeLimit=0", "-C", r.Target,
		"pack-objects", "--quiet", "--non-empty", "--no-reuse-delta", "--no-reuse-object", filepath.Join(r.directory, "index")}
	spec := git.processSpec(args, r.Target, 10*time.Minute)
	spec.Stdin = []byte(strings.Join(ids, "\n") + "\n")
	packed, err := git.runProcess(ctx, spec)
	if err != nil || packed.ExitCode != 0 {
		return &GitException{Message: "保存 Git 暂存对象失败", Result: packed, Cause: err}
	}
	hash := strings.TrimSpace(packed.Stdout)
	if !validGitObjectID(hash) || len(hash) != len(r.Head) {
		return errors.New("无法确认已保存的 Git 暂存对象包")
	}
	r.IndexPack = &workspaceRecyclePack{Hash: hash, Objects: ids}
	packPath := r.packPath()
	indexPath := strings.TrimSuffix(packPath, ".pack") + ".idx"
	if err := checkRecycleOptionalRegular(indexPath); err != nil {
		return err
	}
	verified, err := git.RunContext(ctx, r.Target, 10*time.Minute, true, true, "verify-pack", "--verbose", "--", indexPath)
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(verified.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !validGitObjectID(fields[0]) {
			continue
		}
		if fields[1] != "blob" || !objects[fields[0]] {
			return errors.New("Git 对象包与原工作区索引不一致")
		}
		delete(objects, fields[0])
	}
	if len(objects) != 0 {
		return errors.New("Git 对象包缺少暂存对象")
	}
	r.IndexPack.Digest, err = copyRecycleFile(ctx, packPath, "", 0)
	if err != nil {
		return err
	}
	// Git publishes packs read-only. Windows needs a writable handle to
	// flush the saved private copy to disk.
	if err := os.Chmod(packPath, 0o600); err != nil {
		return err
	}
	file, err := os.OpenFile(packPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return err
	}
	return os.Remove(indexPath)
}

func (r *workspaceRecycleRecord) indexPackDestination(ctx context.Context, git *GitClient) (string, error) {
	if err := checkRecycleDirectory(r.RepositoryRoot, r.RepositoryIdentity); err != nil {
		return "", err
	}
	if err := checkRecycleDirectory(r.CommonDirectory, r.CommonIdentity); err != nil {
		return "", err
	}
	objects, err := git.RunContext(ctx, r.RepositoryRoot, 2*time.Minute, true, true, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	objectRoot := filepath.Join(r.CommonDirectory, "objects")
	if err != nil || !sameRecyclePath(strings.TrimSpace(objects.Stdout), objectRoot) {
		return "", fmt.Errorf("Git 对象目录与原仓库不匹配：%s", r.RepositoryRoot)
	}
	if _, err := documents.DirectoryFingerprint(objectRoot); err != nil {
		return "", err
	}
	packDirectory := filepath.Join(objectRoot, "pack")
	if _, err := documents.DirectoryFingerprint(packDirectory); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return packDirectory, nil
}
