package desktop

import (
	"context"
	"time"
)

// releaseController owns this feature's state. Its zero value is usable in headless tests.
type releaseController struct{ state releaseState }

type releaseState struct {
	tagGenbuAutoProbe *tagGenbuAutoProbeState
	tagJobs           *tagDesktopJobs
	production        *productionUIState
	tagHistory        *tagHistoryViewState
	tagBuild          *tagBuildViewState
	tagTarget         *tagTargetViewState
}

func (s *releaseController) stopFeature() { s.stopProductionJobs() }

func (s *releaseController) drainFeature(closeTagJobs func(context.Context) error) error {
	return runFeatureCleanup(
		featureCleanupStep{"退出前生产发布收尾未完成", 30 * time.Second, s.closeProductionJobs},
		featureCleanupStep{"退出前 Tag 自动 Genbu 探测清理未完成", 5 * time.Second, s.closeTagGenbuAutoProbe},
		featureCleanupStep{"退出前 Tag 后台任务收尾未完成", 5 * time.Second, closeTagJobs},
	)
}
