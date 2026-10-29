package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
)

func TestTagProbeRoundReadsCandidatesOnceAndNextTickReadsAgain(t *testing.T) {
	config := domain.DefaultAppConfig()
	tasks := []domain.TaskManifest{domain.DefaultTaskManifest()}
	key := tagProbeRoundScope("home", "account", config, tasks)
	var reads atomic.Int32
	load := func(context.Context) (*delivery.TagProbeSnapshot, error) {
		reads.Add(1)
		return &delivery.TagProbeSnapshot{}, nil
	}
	round := NewTagProbeRoundContext(context.Background())
	for range 5 {
		if _, err := readTagProbeRound(round, key, load); err != nil {
			t.Fatal(err)
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("one tick read candidates %d times", reads.Load())
	}
	changedConfig := config
	changedConfig.Theme = domain.ThemeLight
	changedTask := append([]domain.TaskManifest(nil), tasks...)
	changedTask[0].CreatedAt = "recreated"
	for _, changedKey := range []string{
		tagProbeRoundScope("other-home", "account", config, tasks),
		tagProbeRoundScope("home", "other-account", config, tasks),
		tagProbeRoundScope("home", "account", changedConfig, tasks),
		tagProbeRoundScope("home", "account", config, changedTask),
	} {
		if _, err := readTagProbeRound(round, changedKey, load); err == nil {
			t.Fatal("round snapshot crossed home/account/config/task identity")
		}
	}
	if _, err := readTagProbeRound(NewTagProbeRoundContext(context.Background()), key, load); err != nil || reads.Load() != 2 {
		t.Fatalf("next tick reused old candidates: reads=%d err=%v", reads.Load(), err)
	}
}

func TestTagProbeRoundCancellationDoesNotCrossRepositorySubscribers(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	round := NewTagProbeRoundContext(parent)
	first, cancelFirst := context.WithCancel(round)
	started, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	load := func(ctx context.Context) (*delivery.TagProbeSnapshot, error) {
		reads.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &delivery.TagProbeSnapshot{}, nil
	}
	firstDone := make(chan error, 1)
	go func() { _, err := readTagProbeRound(first, "scope", load); firstDone <- err }()
	<-started
	cancelFirst()
	waiter, cancelWaiter := context.WithCancel(round)
	waiterDone := make(chan error, 1)
	go func() { _, err := readTagProbeRound(waiter, "scope", load); waiterDone <- err }()
	cancelWaiter()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled subscriber: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled subscriber waited for another repository's loader")
	}
	healthyDone := make(chan error, 1)
	go func() { _, err := readTagProbeRound(round, "scope", load); healthyDone <- err }()
	close(release)
	if err := <-healthyDone; err != nil || reads.Load() != 1 {
		t.Fatalf("cancelled first loader cancelled siblings or duplicated reads: %d, %v", reads.Load(), err)
	}
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled first subscriber accepted shared result: %v", err)
	}
}

func TestTagProbeRoundSlowRepositoryDoesNotBlockFastStatusAndNextRoundRereads(t *testing.T) {
	a, scope, operations := tagJobProbeFixture(t)
	release, started := make(chan struct{}), make(chan struct{})
	a.tagJobManager().probe = func(ctx context.Context, _ domain.AppConfig, _ []domain.TaskManifest, _ bool, ids []string, emit func(string, domain.TagOperation)) (bool, error) {
		operation := operations["fast"]
		if ids[0] == operations["slow"].OperationID {
			operation = operations["slow"]
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		operation.GenbuStatus.Build = domain.GenbuBuilding
		saveTagProbeFixture(t, scope.TaskPath, operation)
		emit(scope.TaskPath, operation)
		return true, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	round := NewTagProbeRoundContext(ctx)
	slowDone := make(chan error, 1)
	go func() {
		_, err := a.DispatchTagProbe(round, tagProbeFixtureAction(scope, "tag.status", operations["slow"].OperationID), nil)
		slowDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow repository did not start")
	}
	// The second repository must consume the same accepted candidate snapshot.
	// A fresh all-history read would now fail on this unrelated local record.
	corrupt := filepath.Join(scope.TaskPath, "tag-operations", strings.Repeat("f", 32)+".json")
	if err := os.WriteFile(corrupt, []byte("{corrupt-after-round-read"), 0600); err != nil {
		t.Fatal(err)
	}
	fastDone := make(chan error, 1)
	go func() {
		_, err := a.DispatchTagProbe(round, tagProbeFixtureAction(scope, "tag.status", operations["fast"].OperationID), nil)
		fastDone <- err
	}()
	select {
	case err := <-fastDone:
		if err != nil {
			t.Fatalf("fast repository reread round history: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow repository held fast status completion")
	}
	close(release)
	if err := <-slowDone; err != nil {
		t.Fatal(err)
	}
	if _, err := a.DispatchTagProbe(NewTagProbeRoundContext(ctx), tagProbeFixtureAction(scope, "tag.status", operations["fast"].OperationID), nil); err == nil {
		t.Fatal("new tick failed to reread externally changed history")
	}
}
