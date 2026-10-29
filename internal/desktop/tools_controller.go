package desktop

import (
	"context"
	"silverwing-go/internal/application"
	"time"
)

// toolsController owns this feature's state. Its zero value is usable in headless tests.
type toolsController struct{ state toolsState }

type toolsState struct {
	json                    string
	startupDevelopmentTools *startupDevelopmentToolsState
	cliJobs                 *cliJobState
	codexAI                 *codexAIState
	toolTab                 string
	tools                   *toolPageState
	toolJobs                *developerToolJobsState
	toolEnabled             [4]bool
}

type toolsShutdownPorts struct {
	closeCLI   func(context.Context) error
	closeCodex func(context.Context) error
}

func (s *toolsController) drainFeature(ports toolsShutdownPorts) error {
	return runFeatureCleanup(
		featureCleanupStep{"退出前 CLI 进程清理未完成", 10 * time.Second, ports.closeCLI},
		featureCleanupStep{"退出前 Codex 请求清理未完成", 10 * time.Second, ports.closeCodex},
		featureCleanupStep{"退出前工具请求清理未完成", 30 * time.Second, s.closeDeveloperToolJobs},
	)
}

func executeToolOperation(ctx context.Context, backend application.Backend, action application.Action) (application.ActionResult, error) {
	if port, ok := backend.(application.DeveloperTools); ok {
		switch action.Kind {
		case "tool.json":
			request, err := application.JSONToolRequestFromAction(action)
			if err != nil {
				return application.ActionResult{}, err
			}
			return legacyOperation(port.ProcessJSON(ctx, request))
		case "tool.time":
			return legacyOperation(port.ConvertTime(ctx, application.TimeToolRequestFromAction(action)))
		case "tool.diff":
			request, err := application.TextDiffRequestFromAction(action)
			if err != nil {
				return application.ActionResult{}, err
			}
			return legacyOperation(port.CompareText(ctx, request))
		case "tool.ports":
			request, err := application.PortQueryRequestFromAction(action)
			if err != nil {
				return application.ActionResult{}, err
			}
			return legacyOperation(port.ListPorts(ctx, request))
		case "tool.port-stop":
			request, err := application.PortStopRequestFromAction(action)
			if err != nil {
				return application.ActionResult{}, err
			}
			return legacyOperation(port.StopPort(ctx, request))
		}
	}
	return backend.Dispatch(ctx, action)
}
