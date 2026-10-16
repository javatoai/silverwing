package integrations

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestRequirementCacheV2MetadataIsScopedAndPersistent(t *testing.T) {
	service := &Service{paths: storage.NewPaths(t.TempDir())}
	item := domain.ParticipatedWorkItem{
		ProjectKey: "project-a", Type: "bug", ID: "123", URL: "https://project.feishu.cn/space/bug/detail/123",
	}
	key := requirementCacheKey("metadata", `account-a`, item)
	name, email := "Tester", "tester@example.invalid"
	status := "In Progress"
	want := RequirementMetadataReadResult{
		ID: item.ID, ProjectKey: item.ProjectKey, Type: item.Type, Title: &name, Status: &status,
		Participants: domain.RequirementParticipants{
			QCOwners:        []domain.RequirementPerson{{Name: "QA"}},
			ProductManagers: []domain.RequirementPerson{{Name: name, Email: &email}},
		},
	}
	if _, err := service.writeRequirementMetadataCache(key, want, 1234); err != nil {
		t.Fatalf("write metadata cache: %v", err)
	}
	saved, path, err := service.readRequirementMetadataCache(key)
	if err != nil {
		t.Fatalf("read metadata cache: %v", err)
	}
	if saved == nil || saved.Data.ID != want.ID || saved.Data.Title == nil || *saved.Data.Title != name ||
		saved.Data.Status == nil || *saved.Data.Status != status || len(saved.Data.Participants.QCOwners) != 1 || path == "" {
		t.Fatalf("metadata cache did not round trip: %#v", saved)
	}
	if saved.Data.Stale || saved.Data.RefreshError != "" {
		t.Fatalf("transient refresh state leaked into persistent cache: %#v", saved.Data)
	}

	for _, other := range []string{
		requirementCacheKey("metadata", `account-b`, item),
		requirementCacheKey("metadata", `account-a`, domain.ParticipatedWorkItem{ProjectKey: "project-b", Type: item.Type, ID: item.ID, URL: item.URL}),
		requirementCacheKey("metadata", `account-a`, domain.ParticipatedWorkItem{ProjectKey: item.ProjectKey, Type: "story", ID: item.ID, URL: item.URL}),
		requirementCacheKey("metadata", `account-a`, domain.ParticipatedWorkItem{ProjectKey: item.ProjectKey, Type: item.Type, ID: "456", URL: "https://project.feishu.cn/space/bug/detail/456"}),
	} {
		otherSaved, _, readErr := service.readRequirementMetadataCache(other)
		if readErr != nil || otherSaved != nil {
			t.Fatalf("metadata cache crossed identity: key=%q saved=%#v err=%v", other, otherSaved, readErr)
		}
	}

	if filepath.Ext(path) != ".json" {
		t.Fatalf("unexpected cache path: %q", path)
	}
}

func TestRequirementCacheV2IdentitySessionsReuseAndContextIsolation(t *testing.T) {
	service := &Service{paths: storage.NewPaths(t.TempDir())}
	fixtureCLI := "controlled-fixture-cli"
	config := domain.AppConfig{MeegleExecutablePath: &fixtureCLI,
		MeegleProjects: []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}}
	var calls atomic.Int32
	probe := func(context.Context, domain.AppConfig) (string, bool, error) {
		calls.Add(3) // The controlled probe represents profile, auth and user CLI calls.
		return `account-a`, false, nil
	}

	first, loggedOut, err := service.requirementIdentityFor(context.Background(), config, "", false, probe)
	if err != nil || loggedOut || first.raw != `account-a` || first.generation == 0 {
		t.Fatalf("initial identity probe failed: %#v loggedOut=%v err=%v", first, loggedOut, err)
	}
	second, loggedOut, err := service.requirementIdentityFor(context.Background(), config, first.token, false, func(context.Context, domain.AppConfig) (string, bool, error) {
		t.Fatal("same-context ordinary read launched another identity probe")
		return "", false, nil
	})
	if err != nil || loggedOut || second.generation != first.generation || calls.Load() != 3 {
		t.Fatalf("confirmed identity session was not reused: %#v calls=%d err=%v", second, calls.Load(), err)
	}

	changedConfig := config
	changedConfig.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project-b", SimpleName: "space"}}
	third, loggedOut, err := service.requirementIdentityFor(context.Background(), changedConfig, "", false, probe)
	if err != nil || loggedOut || third.context == first.context || calls.Load() != 6 {
		t.Fatalf("configuration context did not get its own probe/session: %#v calls=%d err=%v", third, calls.Load(), err)
	}
	forced, loggedOut, err := service.requirementIdentityFor(context.Background(), config, first.token, true, probe)
	if err != nil || loggedOut || forced.generation == first.generation || calls.Load() != 9 {
		t.Fatalf("force did not probe again: %#v calls=%d err=%v", forced, calls.Load(), err)
	}
}

func TestRequirementCacheV2SameAccountForceProbePreservesInflightReadEpoch(t *testing.T) {
	service := &Service{paths: storage.NewPaths(t.TempDir())}
	fixtureCLI := "controlled-fixture-cli"
	config := domain.AppConfig{MeegleExecutablePath: &fixtureCLI,
		MeegleProjects: []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}}
	probeFor := func(account string) func(context.Context, domain.AppConfig) (string, bool, error) {
		return func(context.Context, domain.AppConfig) (string, bool, error) { return account, false, nil }
	}
	first, loggedOut, err := service.requirementIdentityFor(context.Background(), config, "", false, probeFor("account-a"))
	if err != nil || loggedOut || first.generation == 0 {
		t.Fatalf("initial identity session failed: session=%+v loggedOut=%v err=%v", first, loggedOut, err)
	}

	fetchStarted := make(chan struct{})
	fetchRelease := make(chan struct{})
	fetchResult := make(chan error, 1)
	go func() {
		if err := service.verifyRequirementIdentitySession(context.Background(), config, first.raw, first.generation); err != nil {
			fetchResult <- err
			return
		}
		close(fetchStarted)
		<-fetchRelease
		fetchResult <- service.verifyRequirementIdentitySession(context.Background(), config, first.raw, first.generation)
	}()
	select {
	case <-fetchStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("controlled read did not enter its in-flight fetch phase")
	}

	refreshed, loggedOut, err := service.requirementIdentityFor(context.Background(), config, first.token, true, probeFor("account-a"))
	if err != nil || loggedOut || refreshed.generation != first.generation {
		close(fetchRelease)
		t.Fatalf("same-account refresh changed the active identity epoch: old=%+v refreshed=%+v loggedOut=%v err=%v", first, refreshed, loggedOut, err)
	}
	close(fetchRelease)
	if err := <-fetchResult; err != nil {
		t.Fatalf("same-account force probe invalidated an in-flight read: %v", err)
	}

	changed, loggedOut, err := service.requirementIdentityFor(context.Background(), config, "", true, probeFor("account-b"))
	if err != nil || loggedOut || changed.token == first.token || changed.generation == first.generation {
		t.Fatalf("account change did not create a new identity epoch: changed=%+v loggedOut=%v err=%v", changed, loggedOut, err)
	}
	var identityChanged *RequirementIdentityChangedError
	if err := service.verifyRequirementIdentitySession(context.Background(), config, first.raw, first.generation); !errors.As(err, &identityChanged) {
		t.Fatalf("in-flight read was not rejected after an account change: %v", err)
	}

	changedConfig := config
	changedConfig.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project-b", SimpleName: "space"}}
	contextSession, loggedOut, err := service.requirementIdentityFor(context.Background(), changedConfig, "", false, probeFor("account-b"))
	if err != nil || loggedOut || contextSession.generation == changed.generation {
		t.Fatalf("context change did not retire the prior identity epoch: session=%+v loggedOut=%v err=%v", contextSession, loggedOut, err)
	}
	if err := service.verifyRequirementIdentitySession(context.Background(), config, changed.raw, changed.generation); !errors.As(err, &identityChanged) {
		t.Fatalf("old context identity remained valid after context change: %v", err)
	}
	returned, loggedOut, err := service.requirementIdentityFor(context.Background(), config, "", false, probeFor("account-b"))
	if err != nil || loggedOut || returned.generation == changed.generation {
		t.Fatalf("returning to prior context reused a retired identity epoch: session=%+v loggedOut=%v err=%v", returned, loggedOut, err)
	}
	if err := service.verifyRequirementIdentitySession(context.Background(), config, changed.raw, changed.generation); !errors.As(err, &identityChanged) {
		t.Fatalf("retired identity epoch became valid after context return: %v", err)
	}
	_, loggedOut, err = service.requirementIdentityFor(context.Background(), config, "", true,
		func(context.Context, domain.AppConfig) (string, bool, error) { return "", true, nil })
	if err != nil || !loggedOut {
		t.Fatalf("logout probe did not confirm logged-out state: loggedOut=%v err=%v", loggedOut, err)
	}
	err = service.verifyRequirementIdentitySession(context.Background(), config, returned.raw, returned.generation)
	if !errors.As(err, &identityChanged) || identityChanged.State != requirementIdentityLoggedOut {
		t.Fatalf("in-flight read was not rejected with logged-out state: %v", err)
	}
}

func TestRequirementCacheV2ForcedIdentityProbesCoalesce(t *testing.T) {
	service := &Service{paths: storage.NewPaths(t.TempDir())}
	fixtureCLI := "controlled-fixture-cli"
	config := domain.AppConfig{MeegleExecutablePath: &fixtureCLI,
		MeegleProjects: []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	probe := func(ctx context.Context, _ domain.AppConfig) (string, bool, error) {
		calls.Add(3)
		close(started)
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-release:
			return `account-a`, false, nil
		}
	}

	const consumers = 3
	type result struct {
		session requirementIdentitySession
		err     error
	}
	results := make(chan result, consumers)
	var wg sync.WaitGroup
	for range consumers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, _, err := service.requirementIdentityFor(context.Background(), config, "", true, probe)
			results <- result{session: session, err: err}
		}()
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("identity probe did not start")
	}
	waitForIdentityConsumers(t, service, consumers)
	close(release)
	wg.Wait()
	close(results)

	var generation uint64
	for value := range results {
		if value.err != nil {
			t.Fatalf("coalesced identity probe failed: %v", value.err)
		}
		if generation == 0 {
			generation = value.session.generation
		} else if value.session.generation != generation {
			t.Fatalf("concurrent refreshes received different generations: %d vs %d", generation, value.session.generation)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("expected exactly one three-command identity probe, got %d commands", calls.Load())
	}
}

func TestRequirementCacheV2CancelingOneIdentitySubscriberKeepsProbeAlive(t *testing.T) {
	service := &Service{paths: storage.NewPaths(t.TempDir())}
	fixtureCLI := "controlled-fixture-cli"
	config := domain.AppConfig{MeegleExecutablePath: &fixtureCLI,
		MeegleProjects: []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	probe := func(ctx context.Context, _ domain.AppConfig) (string, bool, error) {
		calls.Add(3)
		close(started)
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-release:
			return `account-a`, false, nil
		}
	}

	canceled, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, _, err := service.requirementIdentityFor(canceled, config, "", true, probe)
		first <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("identity probe did not start")
	}

	second := make(chan resultForIdentityTest, 1)
	go func() {
		session, _, err := service.requirementIdentityFor(context.Background(), config, "", true, probe)
		second <- resultForIdentityTest{session: session, err: err}
	}()
	waitForIdentityConsumers(t, service, 2)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled subscriber returned %v", err)
	}
	key := service.requirementIdentityContext(config)
	service.identityStateMu.Lock()
	request := service.identityRequests[key]
	var sharedContext context.Context
	if request != nil {
		sharedContext = request.ctx
	}
	service.identityStateMu.Unlock()
	if sharedContext == nil || sharedContext.Err() != nil {
		t.Fatalf("canceling one subscriber canceled the shared identity probe: %v", sharedContext)
	}
	close(release)
	value := <-second
	if value.err != nil || value.session.raw != `account-a` || calls.Load() != 3 {
		t.Fatalf("remaining subscriber did not complete shared probe: %#v calls=%d", value, calls.Load())
	}
}

func TestRequirementCacheV2AllCanceledProbeStopsAndCloseJoins(t *testing.T) {
	service := &Service{paths: storage.NewPaths(t.TempDir())}
	fixtureCLI := "controlled-fixture-cli"
	config := domain.AppConfig{MeegleExecutablePath: &fixtureCLI}
	started := make(chan struct{})
	canceled := make(chan struct{})
	probe := func(ctx context.Context, _ domain.AppConfig) (string, bool, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return "", false, ctx.Err()
	}
	consumer, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, err := service.requirementIdentityFor(consumer, config, "", true, probe)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("identity probe did not start")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("last canceled consumer returned %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("identity probe goroutine did not observe last-consumer cancellation")
	}
	key := service.requirementIdentityContext(config)
	service.identityStateMu.Lock()
	_, stillRunning := service.identityRequests[key]
	service.identityStateMu.Unlock()
	if stillRunning {
		t.Fatal("identity request remained registered after all consumers canceled")
	}

	service = &Service{paths: storage.NewPaths(t.TempDir())}
	started = make(chan struct{})
	probeDone := make(chan struct{})
	probe = func(ctx context.Context, _ domain.AppConfig) (string, bool, error) {
		close(started)
		<-ctx.Done()
		close(probeDone)
		return "", false, ctx.Err()
	}
	result = make(chan error, 1)
	go func() {
		_, _, err := service.requirementIdentityFor(context.Background(), config, "", true, probe)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown identity probe did not start")
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer closeCancel()
	if err := service.CloseRequirementDocuments(closeCtx); err != nil {
		t.Fatalf("close did not join identity probe: %v", err)
	}
	select {
	case <-probeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown left identity probe goroutine running")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown caller returned %v", err)
	}
	if _, _, err := service.requirementIdentityFor(context.Background(), config, "", false, probe); err == nil {
		t.Fatal("closed service accepted a new identity request")
	}
}

type resultForIdentityTest struct {
	session requirementIdentitySession
	err     error
}

func waitForIdentityConsumers(t *testing.T, service *Service, expected int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		service.identityStateMu.Lock()
		for _, request := range service.identityRequests {
			if request.consumers == expected {
				service.identityStateMu.Unlock()
				return
			}
		}
		service.identityStateMu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("identity probe did not reach %d consumers", expected)
}
