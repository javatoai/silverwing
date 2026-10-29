package production

import (
	"context"
	"errors"

	"silverwing-go/internal/workspace"
)

type DiffResult struct {
	Files   workspace.CompareFilesResult       `json:"files"`
	Preview *workspace.WorkspaceGitFilePreview `json:"preview,omitempty"`
	Commits string                             `json:"commits"`
}

// Difference is intentionally independent of task selection and batch writes.
// Source/target commits remain immutable even if refs move while the dialog is open.
func (s *Service) Difference(ctx context.Context, target Target, sourceSHA, targetSHA string, incoming bool, change *workspace.CompareChange) (DiffResult, error) {
	// Legacy callers do not own a close operation, so retain the one-shot
	// contract even when this service also serves session-aware consumers.
	reader := *s
	reader.DiffSessions = nil
	return reader.DifferenceSession(ctx, target, sourceSHA, targetSHA, incoming, "", change)
}

func (s *Service) DifferenceSession(ctx context.Context, target Target, sourceSHA, targetSHA string, incoming bool, sessionID string, change *workspace.CompareChange) (DiffResult, error) {
	var result DiffResult
	err := s.locks.WithRepository(target.CommonDirectory, func() error {
		if _, err := s.validate(ctx, target); err != nil {
			return err
		}
		client, ok := s.Git.(*workspace.GitClient)
		if !ok {
			return errors.New("当前 Git 读取器不支持文件差异预览")
		}
		// Resolve only full object IDs. Ref names and revision expressions never
		// cross this boundary, including when supplied by a late UI request.
		if !completeSHA(sourceSHA) || !completeSHA(targetSHA) {
			return errors.New("差异提交标识无效，请重新检查")
		}
		for _, sha := range []string{sourceSHA, targetSHA} {
			actual, err := s.commit(ctx, target, sha)
			if err != nil {
				return err
			}
			if actual != sha {
				return errors.New("差异提交身份已变化")
			}
		}
		base, comparison := targetSHA, targetSHA+".."+sourceSHA
		if incoming && (change == nil || sessionID == "") {
			var err error
			base, err = s.run(ctx, target.Repository, "merge-base", targetSHA, sourceSHA)
			if err != nil {
				return err
			}
			comparison = targetSHA + "..." + sourceSHA
		}
		reader := workspace.NewWorkspaceGitDiffService(client, s.locks)
		var err error
		owner := digest(struct {
			Home                 string
			Target               Target
			SourceSHA, TargetSHA string
			Incoming             bool
		}{s.Paths.Home, target, sourceSHA, targetSHA, incoming})
		if s.DiffSessions != nil && change == nil {
			result.Files, err = reader.TreeFilesSessionContext(ctx, s.DiffSessions, owner, target.Repository, base, sourceSHA)
		} else if s.DiffSessions != nil && sessionID != "" && change != nil {
			if incoming {
				base = "" // The owner binds the original pair; use its retained merge-base.
			}
			result.Files, result.Preview, err = reader.TreePreviewSessionContext(ctx, s.DiffSessions, owner, target.Repository, base, sourceSHA, sessionID, *change)
		} else {
			result.Files, result.Preview, err = reader.CompareTreesContext(ctx, target.Repository, base, sourceSHA, change)
		}
		if err != nil {
			return err
		}
		if change == nil {
			args := []string{"log", "--oneline", "--max-count=100"}
			if incoming {
				args = append(args, "--left-right")
			}
			result.Commits, err = s.run(ctx, target.Repository, append(args, comparison)...)
			if err != nil {
				return err
			}
		}
		_, err = s.validate(ctx, target)
		return err
	})
	if err != nil && change == nil && result.Files.SessionID != "" && s.DiffSessions != nil {
		s.DiffSessions.Retire(result.Files.SessionID)
		result.Files.SessionID = ""
	}
	return result, err
}

func completeSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
