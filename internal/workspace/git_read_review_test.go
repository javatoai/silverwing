package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"silverwing-go/internal/process"
)

func TestReviewHistoryNeverConvertsExecutionFailureToBusinessResult(t *testing.T) {
	for _, phase := range []string{"legacy-empty", "unborn-head", "ancestor", "truncated-message"} {
		for _, kind := range []string{"cleanup", "transport", "budget-and-cleanup", "budget-and-transport"} {
			t.Run(phase+"/"+kind, func(t *testing.T) {
				poison := errors.New("unsettled process")
				var failure error = poison
				if strings.HasSuffix(kind, "cleanup") {
					failure = &process.CleanupError{Phase: "test cleanup", Cause: poison}
				}
				if strings.HasPrefix(kind, "budget-and-") {
					failure = errors.Join(process.ErrOutputLimit, failure)
				}
				g := NewGitClient(nil, "")
				sha := strings.Repeat("a", 40)
				g.readCommand = func(_ context.Context, spec process.Spec) (process.Result, error) {
					command := strings.Join(spec.Arguments, " ")
					if strings.Contains(command, " log ") {
						return process.Result{ExitCode: 128, Stderr: "fatal: your current branch does not have any commits yet"}, failure
					}
					if strings.Contains(command, "rev-parse") {
						if phase == "unborn-head" {
							return process.Result{ExitCode: 1}, failure
						}
						return process.Result{ExitCode: 0, Stdout: sha + "\n"}, nil
					}
					if strings.Contains(command, "merge-base") {
						if phase == "ancestor" {
							return process.Result{ExitCode: 1}, failure
						}
						return process.Result{}, nil
					}
					if strings.Contains(command, " show ") {
						return process.Result{ExitCode: 1, Stdout: "truncated", StdoutTruncated: true}, failure
					}
					t.Fatalf("unexpected git command: %s", command)
					return process.Result{}, nil
				}
				service := NewWorkspaceGitHistoryService(g)
				var err error
				switch phase {
				case "legacy-empty":
					_, err = service.ReadContext(context.Background(), t.TempDir(), "")
				case "unborn-head":
					_, _, err = service.historySnapshot(context.Background(), t.TempDir(), "")
				default:
					_, err = service.ReadMessageContext(context.Background(), t.TempDir(), "", WorkspaceGitHistoryPageRequest{HeadSHA: sha}, sha)
				}
				if !errors.Is(err, poison) {
					t.Fatalf("%s hid process failure: %v", phase, err)
				}
				if strings.HasSuffix(kind, "cleanup") && !process.IsCleanupFailure(err) {
					t.Fatalf("cleanup identity lost: %v", err)
				}
			})
		}
	}
}
func TestReviewLocalMaterialsPreparationEmitsOneProgressPair(t *testing.T) {
	service, config, _ := optionalServiceFixture(t)
	events := []TaskCreationProgress{}
	_, err := service.CreateTaskWithProgress(context.Background(), config, CreateTaskRequest{FolderName: "one-local-materials"}, func(e TaskCreationProgress) {
		if e.Phase == TaskCreationPhasePreparingMaterials {
			events = append(events, e)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Status != TaskCreationProgressStarted || events[1].Status != TaskCreationProgressCompleted {
		t.Fatalf("local materials repeated stages: %+v", events)
	}
}

func TestReviewHistoryRetainsExpectedGitExitAndTruncationBehavior(t *testing.T) {
	for _, phase := range []string{"legacy-empty", "unborn-head", "ancestor", "truncated-message"} {
		t.Run(phase, func(t *testing.T) {
			g := NewGitClient(nil, "")
			sha := strings.Repeat("a", 40)
			g.readCommand = func(_ context.Context, spec process.Spec) (process.Result, error) {
				command := strings.Join(spec.Arguments, " ")
				if strings.Contains(command, " log ") {
					return process.Result{ExitCode: 128, Stderr: "fatal: branch does not have any commits yet"}, nil
				}
				if strings.Contains(command, "rev-parse") {
					if phase == "unborn-head" {
						return process.Result{ExitCode: 1}, nil
					}
					return process.Result{ExitCode: 0, Stdout: sha + "\n"}, nil
				}
				if strings.Contains(command, "merge-base") {
					if phase == "ancestor" {
						return process.Result{ExitCode: 1}, nil
					}
					return process.Result{}, nil
				}
				if strings.Contains(command, " show ") {
					return process.Result{ExitCode: 1, Stdout: "message", StdoutTruncated: true}, process.ErrOutputLimit
				}
				t.Fatalf("unexpected git command: %s", command)
				return process.Result{}, nil
			}
			service := NewWorkspaceGitHistoryService(g)
			switch phase {
			case "legacy-empty":
				rows, err := service.ReadContext(context.Background(), t.TempDir(), "")
				if err != nil || len(rows) != 0 {
					t.Fatalf("empty legacy changed: %v", err)
				}
			case "unborn-head":
				head, base, err := service.historySnapshot(context.Background(), t.TempDir(), "")
				if err != nil || head != "" || base != "" {
					t.Fatalf("unborn changed: %v", err)
				}
			case "ancestor":
				_, err := service.ReadMessageContext(context.Background(), t.TempDir(), "", WorkspaceGitHistoryPageRequest{HeadSHA: sha}, sha)
				if err == nil || !strings.Contains(err.Error(), "不属于") {
					t.Fatalf("ordinary nonancestor changed: %v", err)
				}
			default:
				message, err := service.ReadMessageContext(context.Background(), t.TempDir(), "", WorkspaceGitHistoryPageRequest{HeadSHA: sha}, sha)
				if err != nil || !message.Truncated || message.Message != "message" {
					t.Fatalf("allowed truncation changed: %+v %v", message, err)
				}
			}
		})
	}
}
