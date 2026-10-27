package integrations

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

const commentAssetLimit = 100 << 20

func commentRandomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func commentValidID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16
}

// The native image reader validates extensions. Derive them only from the
// decoded image MIME, never from an untrusted original filename or path.
func commentAssetFilename(a domain.CommentDraftAsset) string {
	suffix := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}[a.MIME]
	if suffix == "" {
		suffix = ".bin"
	}
	return a.ID + suffix
}

func (s *Service) commentScope(ctx context.Context, config domain.AppConfig) string {
	profile := ""
	if home, err := os.UserHomeDir(); err == nil {
		if info, err := os.Stat(filepath.Join(home, ".meegle", "config.json")); err == nil {
			profile = fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
		}
	}
	return requirementCacheDigest(s.requirementIdentityContextWithContext(ctx, config) + "\x00" + profile)
}

func (s *Service) commentDirectory(d domain.RequirementCommentDraft) (string, error) {
	if !validOpaqueRequirementIdentity(d.Identity) {
		return "", errors.New("评论草稿账号无效")
	}
	root := filepath.Join(s.paths.Cache, "requirement-comment-drafts")
	if err := ensureRequirementDirectory(s.paths.Home, root); err != nil {
		return "", err
	}
	key := d.Identity + "\x00" + d.Item.Key()
	if d.Editing() {
		key += "\x00edit\x00" + d.Edit.CommentID
	}
	dir := filepath.Join(root, requirementCacheDigest(key))
	if err := ensureRequirementDirectory(root, dir); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Service) readCommentDraft(identity string, item domain.ParticipatedWorkItem) (domain.RequirementCommentDraft, error) {
	return s.readCommentTargetDraft(identity, item, "")
}

func (s *Service) readCommentTargetDraft(identity string, item domain.ParticipatedWorkItem, commentID string) (domain.RequirementCommentDraft, error) {
	d := domain.RequirementCommentDraft{Identity: identity, Item: item, Images: []domain.CommentDraftAsset{}}
	if commentID != "" {
		d.Edit = &domain.CommentEditTarget{CommentID: commentID}
	}
	dir, err := s.commentDirectory(d)
	if err != nil {
		return d, err
	}
	path := filepath.Join(dir, "draft.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		d.ID = commentRandomID()
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return d, errors.New("评论草稿文件无效")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal(raw, &d); err != nil {
		return d, fmt.Errorf("评论草稿读取失败，原文件已保留：%w", err)
	}
	if d.Identity != identity || d.Item.Key() != item.Key() || !commentValidID(d.ID) || commentDraftTarget(d) != commentID || d.Editing() && d.Edit.Original != nil && d.Edit.Original.ID != commentID {
		return d, errors.New("评论草稿目标不匹配")
	}
	// A restarted process never resumes a potentially issued create request.
	if d.Attempt != nil && d.Attempt.State == "publishing" {
		d.Attempt.State = "unknown"
	}
	return d, nil
}

func (s *Service) saveCommentDraft(d *domain.RequirementCommentDraft) error {
	dir, err := s.commentDirectory(*d)
	if err != nil {
		return err
	}
	copy := *d
	copy.Images = append([]domain.CommentDraftAsset{}, d.Images...)
	for i := range copy.Images {
		copy.Images[i].LocalPath, copy.Images[i].Error = "", ""
	}
	if d.Attachment != nil {
		a := *d.Attachment
		a.LocalPath, a.Error = "", ""
		copy.Attachment = &a
	}
	copy.Revision++
	raw, err := json.MarshalIndent(copy, "", "  ")
	if err != nil {
		return err
	}
	if err = storage.WriteFileAtomic(filepath.Join(dir, "draft.json"), raw, 0600); err != nil {
		return fmt.Errorf("评论草稿保存失败，当前内容已保留：%w", err)
	}
	d.Revision = copy.Revision
	return nil
}

func (s *Service) commentAssetPath(d domain.RequirementCommentDraft, a domain.CommentDraftAsset) (string, error) {
	if !commentValidID(a.ID) {
		return "", errors.New("评论文件标识无效")
	}
	dir, err := s.commentDirectory(d)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, commentAssetFilename(a))
	if err := requirementSafePath(dir, path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() != a.Size || info.Size() > commentAssetLimit {
		return "", errors.New("草稿文件已变化，请移除后重新添加")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	if hex.EncodeToString(h.Sum(nil)) != a.Hash {
		return "", errors.New("草稿文件校验失败，请重新添加")
	}
	return path, nil
}

func (s *Service) commentDraftView(d domain.RequirementCommentDraft) domain.RequirementCommentDraft {
	d.Images = append([]domain.CommentDraftAsset{}, d.Images...)
	for i := range d.Images {
		path, err := s.commentAssetPath(d, d.Images[i])
		d.Images[i].LocalPath = path
		if err != nil {
			d.Images[i].Error = err.Error()
		}
	}
	if d.Attachment != nil {
		a := *d.Attachment
		path, err := s.commentAssetPath(d, a)
		a.LocalPath = path
		if err != nil {
			a.Error = err.Error()
		}
		d.Attachment = &a
	}
	return d
}

func (s *Service) removeCommentDraftAssets(d domain.RequirementCommentDraft) {
	dir, err := s.commentDirectory(d)
	if err != nil {
		return
	}
	assets := append([]domain.CommentDraftAsset{}, d.Images...)
	if d.Attachment != nil {
		assets = append(assets, *d.Attachment)
	}
	for _, a := range assets {
		if commentValidID(a.ID) {
			_ = os.Remove(filepath.Join(dir, commentAssetFilename(a)))
		}
	}
}

func (s *Service) stageCommentAsset(d domain.RequirementCommentDraft, source, name string, png []byte, isImage bool) (domain.CommentDraftAsset, error) {
	a := domain.CommentDraftAsset{ID: commentRandomID()}
	var reader io.Reader
	if png != nil {
		reader = bytes.NewReader(png)
		a.Name = "截图.png"
	} else {
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			return a, err
		}
		if !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(source)) {
			return a, errors.New("不能添加符号链接文件")
		}
		info, err := os.Lstat(source)
		if err != nil {
			return a, err
		}
		if !info.Mode().IsRegular() || info.Size() > commentAssetLimit {
			return a, errors.New("请选择不超过 100 MB 的普通文件")
		}
		f, err := os.Open(source)
		if err != nil {
			return a, err
		}
		defer f.Close()
		reader = f
		a.Name = filepath.Base(source)
	}
	if name != "" {
		a.Name = filepath.Base(name)
	}
	data, err := io.ReadAll(io.LimitReader(reader, commentAssetLimit+1))
	if err != nil {
		return a, err
	}
	if len(data) > commentAssetLimit {
		return a, errors.New("文件不能超过 100 MB")
	}
	return s.stageCommentAssetBytes(d, a, data, isImage)
}

func (s *Service) stageCommentAssetBytes(d domain.RequirementCommentDraft, a domain.CommentDraftAsset, data []byte, isImage bool) (domain.CommentDraftAsset, error) {
	if len(data) > commentAssetLimit {
		return a, errors.New("文件不能超过 100 MB")
	}
	a.Size = int64(len(data))
	digest := sha256.Sum256(data)
	a.Hash = hex.EncodeToString(digest[:])
	a.MIME = mime.TypeByExtension(filepath.Ext(a.Name))
	if a.MIME == "" {
		a.MIME = http.DetectContentType(data)
	}
	if isImage {
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 32000000 {
			return a, errors.New("图片格式无效或超过 3200 万像素")
		}
		a.MIME = "image/" + format
		if format == "jpg" {
			a.MIME = "image/jpeg"
		}
	}
	dir, err := s.commentDirectory(d)
	if err != nil {
		return a, err
	}
	if err = storage.WriteFileAtomic(filepath.Join(dir, commentAssetFilename(a)), data, 0600); err != nil {
		return a, err
	}
	return a, nil
}

func commentExpectedDraft(d domain.RequirementCommentDraft, p map[string]any) error {
	revision := fmt.Sprint(p["revision"])
	if d.ID != stringValue(p, "draftID") || revision != strconv.FormatUint(d.Revision, 10) {
		return errors.New("评论草稿已变化，请重新打开后操作；当前输入已保留")
	}
	return nil
}

func (s *Service) requirementCommentDraft(ctx context.Context, config domain.AppConfig, p map[string]any) (any, error) {
	if stringValue(p, "editCommentID") != "" {
		return s.requirementCommentEditDraft(ctx, config, p)
	}
	item, err := s.requirementTarget(ctx, config, p)
	if err != nil {
		return nil, err
	}
	expected, _, err := expectedRequirementIdentity(p)
	if err != nil {
		return nil, err
	}
	op := stringValue(p, "operation")
	if op == "" {
		op = "load"
	}
	session, loggedOut, err := s.requirementIdentityFor(ctx, config, expected, op == "load", s.requirementCacheIdentity)
	if err != nil {
		return nil, err
	}
	if loggedOut || session.token == "" {
		return nil, errors.New("请先登录 Meegle 账号")
	}
	release, err := s.acquireRequirementCommentLock(ctx, item)
	if err != nil {
		return nil, err
	}
	defer release()
	d, err := s.readCommentDraft(session.token, item)
	if err != nil {
		return nil, err
	}
	original := d
	scope := s.commentScope(ctx, config)
	if op != "load" {
		if err = commentExpectedDraft(d, p); err != nil {
			return nil, err
		}
		if d.Scope != scope || stringValue(p, "scope") != scope {
			return nil, errors.New("账号或配置已变化，请重新打开评论草稿")
		}
		if d.Pending() && op != "allow-retry" {
			return nil, errors.New("发布结果待核对，请先核对发布结果")
		}
	}
	switch op {
	case "load":
		var identity []string
		_ = json.Unmarshal([]byte(session.raw), &identity)
		if len(identity) < 5 {
			return nil, errors.New("当前 Meegle 账号信息不完整")
		}
		d.Account = identity[4]
		user, e := s.runMeegle(ctx, config, []string{"user", "me", "--format", "json"}, 15*time.Second, "读取评论账号")
		if e != nil {
			return nil, e
		}
		who, e := decodeJSON(user.Stdout)
		if e != nil || text(object(who)["user_key"]) != identity[4] {
			return nil, errors.New("评论账号已变化，请重新打开")
		}
		if name := firstFieldText(object(who), "name_cn", "name", "name_en"); name != "" {
			d.Account = name + "（" + identity[4] + "）"
		}
		d.Scope = scope
		d.Item = item
		if err = s.saveCommentDraft(&d); err != nil {
			return nil, err
		}
	case "save":
		body, _ := p["content"].(string)
		if len(body) > 2<<20 {
			return nil, errors.New("评论文字不能超过 2 MB")
		}
		d.Content = body
		d.Attempt = nil
	case "add-image", "add-attachment", "paste":
		if op == "add-attachment" && d.Attachment != nil {
			return nil, errors.New("每条评论只能添加一个附件，请先移除原附件")
		}
		if op != "add-attachment" && len(d.Images) >= 20 {
			return nil, errors.New("每条评论最多添加 20 张图片")
		}
		png, _ := p["imagePNG"].([]byte)
		if op == "paste" && len(png) == 0 {
			return nil, errors.New("剪贴板中没有截图")
		}
		a, e := s.stageCommentAsset(d, stringValue(p, "sourcePath"), "", png, op != "add-attachment")
		if e != nil {
			return nil, e
		}
		if op == "add-attachment" {
			d.Attachment = &a
		} else {
			d.Images = append(d.Images, a)
		}
		d.Attempt = nil
	case "remove":
		id := stringValue(p, "assetID")
		images := []domain.CommentDraftAsset{}
		for _, a := range d.Images {
			if a.ID != id {
				images = append(images, a)
			}
		}
		d.Images = images
		if d.Attachment != nil && d.Attachment.ID == id {
			d.Attachment = nil
		}
		d.Attempt = nil
	case "clear":
		if !boolValue(p, "confirmed") {
			return nil, errors.New("清空草稿需要确认")
		}
		d.ID = commentRandomID()
		d.Content = ""
		d.Images = []domain.CommentDraftAsset{}
		d.Attachment = nil
		d.Attempt = nil
	case "allow-retry":
		if !boolValue(p, "confirmed") || !d.Pending() || d.Attempt.ID != stringValue(p, "attemptID") {
			return nil, errors.New("请确认原评论未发布，再重新发布")
		}
		// Once a create may have used a receipt, it must not authorize another
		// create. Keep receipts while pending so read-only reconciliation can
		// identify the original comment; an explicitly confirmed new attempt
		// keeps the staged files but obtains fresh credentials for every asset.
		d.Images = append([]domain.CommentDraftAsset{}, d.Images...)
		for i := range d.Images {
			d.Images[i].Upload = nil
		}
		if d.Attachment != nil {
			attachment := *d.Attachment
			attachment.Upload = nil
			d.Attachment = &attachment
		}
		d.Attempt = nil
	default:
		return nil, errors.New("未知评论草稿操作")
	}
	if op != "load" {
		if err = s.saveCommentDraft(&d); err != nil {
			return nil, err
		}
		keep := map[string]bool{}
		for _, a := range d.Images {
			keep[a.ID] = true
		}
		if d.Attachment != nil {
			keep[d.Attachment.ID] = true
		}
		obsolete := domain.RequirementCommentDraft{Identity: original.Identity, Item: original.Item, Edit: original.Edit}
		for _, a := range original.Images {
			if !keep[a.ID] {
				obsolete.Images = append(obsolete.Images, a)
			}
		}
		if original.Attachment != nil && !keep[original.Attachment.ID] {
			obsolete.Attachment = original.Attachment
		}
		s.removeCommentDraftAssets(obsolete)
	}
	return s.commentDraftView(d), nil
}
