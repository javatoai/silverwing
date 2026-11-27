package production

import (
	"context"
	"errors"

	"silverwing-go/internal/domain"
)

// PreviewRelease reads the requested branch and the current production baseline.
// A plan with ReleaseSHA set requires explicit branch reuse confirmation.
func (s *Service) PreviewRelease(ctx context.Context, preview Preview, version string, allowReuse bool) (ReleaseBranchPlan, error) {
	if _, err := parseBaseVersion(version); err != nil {
		return ReleaseBranchPlan{}, err
	}
	branch := "release/" + version
	var plan ReleaseBranchPlan
	err := s.withTargetLock(ctx, preview.Target, func(_ domain.AppConfig) error {
		fresh, c, err := s.observeMatchingBaseline(ctx, preview, branch)
		if err != nil {
			return err
		}
		var reusedBranch string
		if allowReuse && fresh.ReleaseSHA != "" {
			reusedBranch = branch
		}
		if err = validateReleaseInitialization(fresh, c, version, reusedBranch); err != nil {
			return err
		}
		fresh.Commits, fresh.Diff = preview.Commits, preview.Diff
		fresh.ProductionStatus = preview.ProductionStatus
		plan = ReleaseBranchPlan{Version: version, Preview: fresh}
		return nil
	})
	return plan, err
}

// ReuseRelease links the confirmed remote commit to the batch through a local
// record write. It does not create, merge or push a Git branch.
func (s *Service) ReuseRelease(ctx context.Context, preview Preview, version, expectedID string) (Batch, error) {
	if _, err := parseBaseVersion(version); err != nil {
		return Batch{}, err
	}
	branch := "release/" + version
	if preview.ReleaseSHA == "" || preview.BranchHeads[branch] != preview.ReleaseSHA {
		return Batch{}, errors.New("缺少已确认的远端 release 分支和提交，请重新检查")
	}
	return s.initialize(ctx, preview, version, false, expectedID, branch)
}

func validateReleaseInitialization(p Preview, c domain.AppConfig, version, reusedBranch string) error {
	if err := validateVersion(version, p, reusedBranch); err != nil {
		return err
	}
	branch := "release/" + version
	if branch == p.Target.MainBranch {
		return errors.New("release 分支不能与服务主分支相同")
	}
	operation := "创建生产 release"
	if reusedBranch != "" {
		operation = "复用生产 release"
	}
	if err := domain.NewGitWritePolicy(c.BlockedGitWriteBranches).RequireAllowed(branch, operation); err != nil {
		return err
	}
	if !p.ProductionInMain {
		return errors.New("生产 Tag 尚未合入主分支，请先点击合并生产 Tag")
	}
	return nil
}
