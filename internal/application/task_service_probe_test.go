package application

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/production"
)

func setTaskServicesProbe(t *testing.T, a *App, enabled bool) {
	t.Helper()
	cfg, err := a.Config.LoadForPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Groups[0].Services[0].GenbuProbeEnabled = enabled
	if err := a.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestTaskServicesDisabledProbeNeverCallsReader(t *testing.T) {
	for _, environment := range []string{"uat", "prod"} {
		for _, kind := range []string{"task.services.pods", "task.services.config"} {
			t.Run(environment+"/"+kind, func(t *testing.T) {
				a, action := taskServicesFixture(t)
				setTaskServicesProbe(t, a, false)
				action.Kind, action.Payload["environment"] = kind, environment
				calls := 0
				reader := taskServicesFake{
					pods: func(context.Context, string, string) (production.ServicePods, error) {
						calls++
						return production.ServicePods{}, nil
					},
					config: func(context.Context, string, string, string) (string, error) {
						calls++
						return "safe", nil
					},
				}
				_, err := a.taskServicesActionWithReader(context.Background(), action, reader)
				if err == nil || !strings.Contains(err.Error(), "探测") || calls != 0 {
					t.Fatalf("disabled service reached reader: calls=%d, err=%v", calls, err)
				}
			})
		}
	}
}

// Announce the capacity wait only after the first ownership validation. Other
// context consumers (configuration locks, file reads) must not release this gate.
type taskServicesQueueContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *taskServicesQueueContext) Done() <-chan struct{} {
	if pc, _, _, ok := runtime.Caller(1); ok {
		if f := runtime.FuncForPC(pc); f != nil && strings.HasSuffix(f.Name(), ".taskServicesActionWithReader") {
			ctx.once.Do(func() { close(ctx.waiting) })
		}
	}
	return ctx.Context.Done()
}

func TestTaskServicesDisableWhileQueuedNeverReachesReader(t *testing.T) {
	for _, kind := range []string{"task.services.pods", "task.services.config"} {
		t.Run(kind, func(t *testing.T) {
			a, action := taskServicesFixture(t)
			action.Kind = kind
			held := cap(taskServiceReadSlots)
			for i := 0; i < cap(taskServiceReadSlots); i++ {
				taskServiceReadSlots <- struct{}{}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			queued := &taskServicesQueueContext{Context: ctx, waiting: make(chan struct{})}
			var calls int
			fake := taskServicesFake{
				pods: func(context.Context, string, string) (production.ServicePods, error) {
					calls++
					return production.ServicePods{}, nil
				},
				config: func(context.Context, string, string, string) (string, error) { calls++; return "safe", nil },
			}
			done := make(chan struct{})
			var resultErr error
			go func() { _, resultErr = a.taskServicesActionWithReader(queued, action, fake); close(done) }()
			defer func() {
				cancel()
				<-done
				for i := 0; i < held; i++ {
					<-taskServiceReadSlots
				}
			}()
			select {
			case <-queued.waiting:
				setTaskServicesProbe(t, a, false)
			case <-ctx.Done():
				t.Fatal("request never reached concurrency wait")
			}
			<-taskServiceReadSlots // Permit just the queued request to proceed.
			held--
			<-done
			if resultErr == nil || !strings.Contains(resultErr.Error(), "探测") || calls != 0 {
				t.Fatal("queued service ignored disabled flag", calls, resultErr)
			}
		})
	}
}

func TestTaskServicesDisableRejectsLateResultAndReenableWorks(t *testing.T) {
	for _, kind := range []string{"task.services.pods", "task.services.config"} {
		t.Run(kind, func(t *testing.T) {
			a, action := taskServicesFixture(t)
			action.Kind = kind
			fake := taskServicesFake{
				pods: func(context.Context, string, string) (production.ServicePods, error) {
					setTaskServicesProbe(t, a, false)
					return production.ServicePods{}, nil
				},
				config: func(context.Context, string, string, string) (string, error) {
					setTaskServicesProbe(t, a, false)
					return "discard late config", nil
				},
			}
			result, err := a.taskServicesActionWithReader(context.Background(), action, fake)
			if err == nil || result.Data != nil {
				t.Fatal("disabled service published a late result", result, err)
			}
			setTaskServicesProbe(t, a, true)
			result, err = a.taskServicesActionWithReader(context.Background(), action, taskServicesFake{})
			if err != nil || result.Data == nil {
				t.Fatal("reenabling probe did not restore queries", result, err)
			}
		})
	}
}
