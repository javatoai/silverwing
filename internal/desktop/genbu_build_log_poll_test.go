package desktop

import (
	"context"
	"testing"
	"time"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

func TestGenbuBuildLogPollsEveryTwoSecondsWhileOpen(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	calls := genbuPendingReads(t, s)
	s.openGenbuBuildLog("Example", "example", "PROD", "1.0.0", func() bool { return true },
		application.Action{Kind: "production.build-log", Payload: map[string]any{"tag": "1.0.0"}})
	d := s.genbuBuildLog
	first := genbuAwaitRead(t, calls)
	if !d.followTail || first.action.Payload["tailLines"] != 1000 {
		t.Fatal("log opens without the default follow state or tail")
	}
	tester := ui.NewTester(func(c *ui.Context) {
		s.renderGenbuReadDialogs(c, setTheme(c, false))
	}, 980, 650)
	request := d.logRequest
	tester.Advance(2 * time.Second)
	if d.logRequest != request || first.ctx.Err() != nil {
		t.Fatal("poll interrupted an active read")
	}

	cancelGenbuRead(&d.logRequest)
	d.followTail, d.tail = false, " 25 "
	tester.Advance(1999 * time.Millisecond)
	if d.logRequest != nil {
		t.Fatal("log was refreshed before the two-second interval")
	}
	tester.Advance(time.Millisecond)
	second := genbuAwaitRead(t, calls)
	if second.action.Payload["tailLines"] != 1000 || d.tail != " 25 " {
		t.Fatal("poll used or rewrote an unapplied tail input")
	}

	request = d.logRequest
	s.readGenbuBuildLog(d)
	if d.logRequest != request || second.ctx.Err() != nil || d.tail != "25" {
		t.Fatal("submitting the tail interrupted the active read or failed to apply the input")
	}
	cancelGenbuRead(&d.logRequest)
	tester.Advance(2 * time.Second)
	third := genbuAwaitRead(t, calls)
	if third.action.Payload["tailLines"] != 25 {
		t.Fatal("poll omitted the applied tail")
	}

	s.stopTaskServices()
	next := d.nextPoll
	tester.Advance(4 * time.Second)
	if s.genbuBuildLog != nil || third.ctx.Err() != context.Canceled || d.logRequest != nil || d.nextPoll != next {
		t.Fatal("shutdown did not cancel the active read and end polling")
	}
}
