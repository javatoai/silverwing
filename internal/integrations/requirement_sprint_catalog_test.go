package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func sprintCatalogTestService(t *testing.T) (*Service, domain.AppConfig, requirementIdentitySession) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	store := storage.NewConfigStore(paths)
	config := domain.DefaultAppConfig()
	cli := "controlled-sprint-catalog-fixture"
	config.MeegleExecutablePath = &cli
	config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "p", SimpleName: "space"}}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(paths, store, nil)
	session := sprintCatalogTestIdentity(t, s, config, "account-a")
	return s, config, session
}

func sprintCatalogTestIdentity(t *testing.T, s *Service, config domain.AppConfig, account string) requirementIdentitySession {
	t.Helper()
	session, loggedOut, err := s.requirementIdentityFor(context.Background(), config, "", true,
		func(context.Context, domain.AppConfig) (string, bool, error) { return account, false, nil })
	if err != nil || loggedOut {
		t.Fatalf("confirm fixture identity: session=%+v loggedOut=%v err=%v", session, loggedOut, err)
	}
	return session
}

func sprintCatalogTestKeys(t *testing.T, config domain.AppConfig, identity string, sprint *string) participatedListCacheKeys {
	t.Helper()
	payload := map[string]any{}
	if sprint != nil {
		payload["sprintKey"] = *sprint
	}
	keys, err := participatedListCacheKeysFor(config, identity, payload)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func sprintCatalogTestRead(sprints []sprintItem, age time.Duration) requirementSprintCatalogResult {
	started := nextRequirementSprintCatalogRead() - age.Nanoseconds()
	return requirementSprintCatalogResult{Sprints: sprints, FetchedAt: started / int64(time.Millisecond), readStartedAt: started}
}

func TestRequirementSprintCatalogRestartOverlaysEveryLegacyList(t *testing.T) {
	s, config, session := sprintCatalogTestService(t)
	oldSprint := sprintItem{ProjectKey: "p", ProjectName: "space", ID: "1", Title: "Old", Status: "进行中"}
	newSprint := sprintItem{ProjectKey: "p", ProjectName: "space", ID: "2", Title: "Current", Status: "进行中"}
	oldKey, newKey := oldSprint.Key(), newSprint.Key()
	for _, selected := range []*string{nil, &oldKey, &newKey} {
		keys := sprintCatalogTestKeys(t, config, session.token, selected)
		key, _ := keys.requestedKey()
		bound := oldKey
		if selected != nil {
			bound = *selected
		}
		legacy := participatedResult{
			Items:   []domain.ParticipatedWorkItem{{ProjectKey: "p", Type: "bug", ID: "101", Title: bound}},
			Sprints: []sprintItem{oldSprint, newSprint}, SelectedSprintKey: &bound,
			FetchedAt: requirementUnixMillis() - time.Hour.Milliseconds(), CompletedQueries: 4, FromCache: true,
		}
		if err := s.writeRequirementListCache(key, legacy); err != nil {
			t.Fatal(err)
		}
	}
	keys := sprintCatalogTestKeys(t, config, session.token, nil)
	catalog := sprintCatalogTestRead([]sprintItem{newSprint}, time.Second)
	if err := s.persistRequirementSprintCatalog(context.Background(), config, keys, session.generation, catalog); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(s.paths, s.config, nil)
	sprintCatalogTestIdentity(t, restarted, config, "account-a")
	for _, selected := range []*string{nil, &oldKey, &newKey} {
		payload := map[string]any{"expectedIdentity": session.token}
		if selected != nil {
			payload["sprintKey"] = *selected
		}
		value, err := restarted.Execute(context.Background(), "requirements.list", payload)
		if err != nil {
			t.Fatal(err)
		}
		got := value.(participatedResult)
		if !got.FromCache || got.SprintsFetchedAt != catalog.FetchedAt || len(got.Sprints) != 1 || got.Sprints[0].Key() != newKey {
			t.Fatalf("legacy envelope resurrected stale Sprint choices: %+v", got)
		}
		if selected == nil || *selected == oldKey {
			if len(got.Items) != 0 || got.SelectedSprintKey != nil || got.CompletedQueries != 0 {
				t.Fatalf("removed Sprint retained or relabeled cached items: %+v", got)
			}
		} else if len(got.Items) != 1 || got.Items[0].Title != newKey || got.SelectedSprintKey == nil || *got.SelectedSprintKey != newKey {
			t.Fatalf("valid Sprint lost its bound items: %+v", got)
		}
	}
	key, _ := keys.requestedKey()
	saved, _, err := restarted.readRequirementListCache(key)
	if err != nil || saved == nil || saved.Value.FromCache {
		t.Fatalf("fromCache=true reached disk: saved=%+v err=%v", saved, err)
	}
}

func TestRequirementSprintCatalogScopeAndMonotonicPersistence(t *testing.T) {
	s, config, session := sprintCatalogTestService(t)
	keys := sprintCatalogTestKeys(t, config, session.token, nil)
	key, _ := keys.sprintCatalogKey()
	older := sprintCatalogTestRead([]sprintItem{{ProjectKey: "p", ID: "1"}}, 2*time.Second)
	newer := sprintCatalogTestRead([]sprintItem{{ProjectKey: "p", ID: "2"}}, time.Second)
	// A later manual refresh finishes before an older background read.
	for _, read := range []requirementSprintCatalogResult{newer, older} {
		if err := s.persistRequirementSprintCatalog(context.Background(), config, keys, session.generation, read); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := s.readRequirementSprintCatalogCache(key)
	if err != nil || saved == nil || saved.ReadStartedAt != newer.readStartedAt || len(saved.Sprints) != 1 || saved.Sprints[0].ID != "2" {
		t.Fatalf("older background read replaced newer catalog: %+v %v", saved, err)
	}
	otherIdentity := keys
	otherIdentity.Identity = opaqueRequirementIdentity("account-b")
	otherProjects := keys
	otherProjects.ProjectsJSON = `[{"projectKey":"other","simpleName":"other"}]`
	otherDefault := keys
	defaultProject := "other"
	otherDefault.DefaultProject = &defaultProject
	for _, other := range []participatedListCacheKeys{otherIdentity, otherProjects, otherDefault} {
		otherKey, _ := other.sprintCatalogKey()
		cached, err := s.readRequirementSprintCatalogCache(otherKey)
		if err != nil || cached != nil {
			t.Fatalf("catalog crossed identity/config scope: %+v %v", cached, err)
		}
	}
	// A list envelope is never separate evidence of an all-project catalog.
	legacy := participatedResult{Items: []domain.ParticipatedWorkItem{}, Sprints: older.Sprints, FetchedAt: requirementUnixMillis(), SprintsFetchedAt: requirementUnixMillis()}
	s.persistParticipatedListCache(keys, legacy, true)
	saved, err = s.readRequirementSprintCatalogCache(key)
	if err != nil || saved == nil || saved.Sprints[0].ID != "2" {
		t.Fatalf("legacy list promoted itself to authoritative catalog: %+v %v", saved, err)
	}
	// Order must remain strict even for requests whose DTO timestamps match.
	first := nextRequirementSprintCatalogRead()
	second := nextRequirementSprintCatalogRead()
	if second <= first {
		t.Fatalf("catalog request order is not monotonic: %d then %d", first, second)
	}
}

func TestRequirementSprintCatalogRejectsCanceledAndChangedIdentityWrites(t *testing.T) {
	for _, mode := range []string{"canceled", "changed-account", "retired-epoch", "config-changed"} {
		t.Run(mode, func(t *testing.T) {
			s, config, session := sprintCatalogTestService(t)
			keys := sprintCatalogTestKeys(t, config, session.token, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "canceled":
				cancel()
			case "changed-account":
				sprintCatalogTestIdentity(t, s, config, "account-b")
			case "retired-epoch":
				sprintCatalogTestIdentity(t, s, config, "account-b")
				sprintCatalogTestIdentity(t, s, config, "account-a")
			case "config-changed":
				changed := config
				changed.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "other", SimpleName: "other"}}
				if err := s.config.Save(changed); err != nil {
					t.Fatal(err)
				}
			}
			err := s.persistRequirementSprintCatalog(ctx, config, keys, session.generation, sprintCatalogTestRead([]sprintItem{}, time.Second))
			if err == nil || mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("invalidated request was allowed to persist: %v", err)
			}
			files, globErr := filepath.Glob(filepath.Join(s.paths.Cache, "requirement-sprint-catalogs", "*.json"))
			if globErr != nil || len(files) > 0 {
				t.Fatalf("invalidated request wrote a catalog: %v %v", files, globErr)
			}
		})
	}
}

func TestRequirementSprintCatalogPartialAndCompleteEmptySemantics(t *testing.T) {
	s, config, session := sprintCatalogTestService(t)
	keys := sprintCatalogTestKeys(t, config, session.token, nil)
	old := sprintCatalogTestRead([]sprintItem{{ProjectKey: "p", ID: "1"}}, 2*time.Second)
	if err := s.persistRequirementSprintCatalog(context.Background(), config, keys, session.generation, old); err != nil {
		t.Fatal(err)
	}
	selected := "p:1"
	for _, partial := range [][]sprintItem{nil, {}, {{ProjectKey: "other", ID: "2"}}} {
		live := participatedResult{Items: []domain.ParticipatedWorkItem{{ID: "101"}}, Sprints: partial, SelectedSprintKey: &selected}
		got := s.overlayRequirementSprintCatalog(keys, live)
		if len(got.Sprints) != 1 || got.Sprints[0].ID != "1" || got.SprintsFetchedAt != 0 || got.FromCache || len(got.Items) != 1 {
			t.Fatalf("live partial read claimed completeness or deleted authoritative choices: %+v", got)
		}
	}
	empty := sprintCatalogTestRead([]sprintItem{}, time.Second)
	if err := s.persistRequirementSprintCatalog(context.Background(), config, keys, session.generation, empty); err != nil {
		t.Fatal(err)
	}
	got := s.overlayRequirementSprintCatalog(keys, participatedResult{Items: []domain.ParticipatedWorkItem{{ID: "101"}}, SelectedSprintKey: &selected, FromCache: true, Sprints: old.Sprints})
	if got.Sprints == nil || len(got.Sprints) != 0 || got.SprintsFetchedAt != empty.FetchedAt || len(got.Items) != 0 || got.SelectedSprintKey != nil {
		t.Fatalf("complete empty catalog did not clear stale selection: %+v", got)
	}
	key, _ := keys.sprintCatalogKey()
	data, err := os.ReadFile(filepath.Join(s.paths.Cache, "requirement-sprint-catalogs", requirementCacheDigest(key)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if choices, ok := envelope["sprints"].([]any); !ok || len(choices) != 0 {
		t.Fatalf("complete empty catalog was serialized as unavailable: %s", data)
	}
}

func TestRequirementSprintCatalogDefaultSelectionRequiresUniqueOngoingSprint(t *testing.T) {
	defaultProject := "p"
	for _, test := range []struct {
		name    string
		sprints []sprintItem
		want    string
	}{
		{name: "single ongoing globally", sprints: []sprintItem{{ProjectKey: "q", ID: "1", Status: "进行中"}}, want: "q:1"},
		{name: "one ongoing in default project", sprints: []sprintItem{{ProjectKey: "p", ID: "1", Status: "进行中"}, {ProjectKey: "q", ID: "2", Status: "进行中"}}, want: "p:1"},
		{name: "two ongoing in default project", sprints: []sprintItem{{ProjectKey: "p", ID: "1", Status: "进行中"}, {ProjectKey: "p", ID: "2", Status: "进行中"}}},
		{name: "only upcoming", sprints: []sprintItem{{ProjectKey: "p", ID: "1", Status: "未开始"}}},
		{name: "none in default project", sprints: []sprintItem{{ProjectKey: "q", ID: "1", Status: "进行中"}, {ProjectKey: "q", ID: "2", Status: "进行中"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := selectDefaultRequirementSprint(test.sprints, &defaultProject)
			if test.want == "" {
				if selected != nil {
					t.Fatalf("ambiguous catalog was auto-selected: %+v", selected)
				}
			} else if selected == nil || selected.Key() != test.want {
				t.Fatalf("unique current Sprint was not selected: got=%+v want=%s", selected, test.want)
			}
		})
	}
}
