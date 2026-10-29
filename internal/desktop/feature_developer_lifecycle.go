package desktop

import (
	"context"
	"time"
)

func (s *app) stopDeveloperFeatures() {
	s.taskUnit.stopTaskOpenTools()
	s.releaseUnit.stopFeature()
	s.toolsUnit.stopStartupDevelopmentTools()
	s.stopServiceRepositoryChoices()
}

func (s *app) drainDeveloperFeatures() error {
	var diffCloser func(context.Context) error
	if closer, ok := s.backend.(interface{ CloseWorkspaceDiffSessions(context.Context) error }); ok {
		diffCloser = closer.CloseWorkspaceDiffSessions
	}
	return runFeatureClosers(
		func() error { return s.releaseUnit.drainFeature(s.closeTagDesktopJobs) },
		func() error {
			return runFeatureCleanup(featureCleanupStep{"退出前开发工具探测清理未完成", 10 * time.Second, s.toolsUnit.closeStartupDevelopmentTools})
		},
		func() error {
			return s.workspaceUnit.drainFeature(workspaceShutdownPorts{closeServiceReads: s.closeServiceRepositoryChoices, closeDiffSessions: diffCloser})
		},
	)
}

func (s *app) drainTaskFeature() error {
	return runFeatureCleanup(
		featureCleanupStep{"退出前任务分支预检清理未完成", 10 * time.Second, s.taskUnit.closeTaskCreationPreflights},
		featureCleanupStep{"退出前任务删除风险读取清理未完成", 10 * time.Second, s.taskUnit.closeTaskDeleteRiskJobs},
	)
}
