package desktop

import (
	"context"
	"fmt"
	"time"
)

// A plan only sequences shutdown. Every owner supplies its own cancellation,
// settlement and timeout policy; material leases and port termination therefore
// keep their different contracts rather than inheriting a universal cancel rule.
type featureCleanupStep struct {
	label   string
	timeout time.Duration
	run     func(context.Context) error
}

func runFeatureCleanup(steps ...featureCleanupStep) error {
	var first error
	for _, step := range steps {
		if step.run == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), step.timeout)
		err := step.run(ctx)
		cancel()
		if first == nil && err != nil {
			if step.label == "" {
				first = err
			} else {
				first = fmt.Errorf("%s：%w", step.label, err)
			}
		}
	}
	return first
}

// Owners drain independently even after an earlier owner reported a failure.
func runFeatureClosers(closers ...func() error) error {
	var first error
	for _, close := range closers {
		if close == nil {
			continue
		}
		if err := close(); first == nil && err != nil {
			first = err
		}
	}
	return first
}

func (s *app) stopDesktopFeatures() {
	s.stopAppUpdates()
	s.stopTaskTerminals()
	s.closeMarkdownLinkFile()
	s.taskFilesUnit.stopTaskFiles()
	s.stopTaskServices()
	s.taskUnit.stopFeature()
	s.materialsUnit.stopFeature()
	s.settingsUnit.stopFeature()
	s.stopDeveloperFeatures()
	s.cancelConfirmationDialog()
	s.stopSystemOpenJobs()
}

// Window shutdown coordinates complete feature entries. Each feature owns the
// detailed worker list, resource state, and late-result cleanup rules above.
func (s *app) closeDesktopFeatures(runErr error) error {
	// Sample positions and finish pending saves before disposing the current
	// component context, including resource imports outside the preview group.
	defer func() {
		if state := s.materialsUnit.state.markdown; state != nil {
			closeMarkdownPreviewAssets(state)
		}
	}()
	closers := []func() error{
		func() error {
			return runFeatureCleanup(featureCleanupStep{"退出前更新检查清理未完成", 5 * time.Second, s.closeAppUpdates})
		},
		func() error {
			return runFeatureCleanup(featureCleanupStep{"退出前任务终端清理未完成", 10 * time.Second, s.closeTaskTerminals})
		},
		func() error {
			return runFeatureCleanup(featureCleanupStep{"退出前服务信息读取清理未完成", 10 * time.Second, s.closeTaskServices})
		},
		func() error {
			return runFeatureCleanup(featureCleanupStep{"退出前任务文件读取清理未完成", 10 * time.Second, s.taskFilesUnit.closeTaskFiles})
		},
		s.materialsUnit.drainFeature,
		s.drainDeveloperFeatures,
		s.settingsUnit.drainFeature,
		func() error {
			return s.requirementUnit.drainFeature(requirementShutdownPorts{
				closeNaming:    s.closeRequirementNamingPreparation,
				closeDocuments: s.closeRequirementDocumentPreparation,
				closeRefresh:   s.closeRequirementRefreshJobs,
			})
		},
		func() error {
			return s.toolsUnit.drainFeature(toolsShutdownPorts{closeCLI: s.closeCLIJobs, closeCodex: s.closeCodexAIJobs})
		},
		s.drainTaskFeature,
		func() error {
			return runFeatureCleanup(featureCleanupStep{"退出前系统打开与定位清理未完成", 30 * time.Second, s.closeSystemOpenJobs})
		},
		s.closeReadingSession,
		s.closeDesktopPreferences,
		func() error {
			if s.cancelOperation != nil {
				s.cancelOperation()
			}
			return s.materialsUnit.finishFeature(s)
		},
		func() error {
			return runFeatureCleanup(featureCleanupStep{"退出前 Mermaid 初始化清理未完成", 10 * time.Second, s.mermaidInitializer.close})
		},
	}
	for _, close := range closers {
		if err := close(); runErr == nil && err != nil {
			runErr = err
		}
	}
	return runErr
}
