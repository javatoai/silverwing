package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
)

func TestReleaseNoticeWholeBatchAndConfigurationGate(t *testing.T) {
	a, scope, ops := tagJobProbeFixture(t)
	cfg, e := a.Config.Load()
	if e != nil {
		t.Fatal(e)
	}
	// No CLI is used: fresh synthetic statuses make all following reads local.
	batch := "notice-batch"
	now := domain.FormatSilverWingTime(time.Now())
	cfg.Repositories = []domain.RepositoryConfig{}
	for name, op := range ops {
		cfg.Repositories = append(cfg.Repositories, domain.RepositoryConfig{ID: name, Name: name, RootPath: scope.TaskPath + "/" + name, GitCommonDirectory: scope.TaskPath + "/" + name + "/.git"})
		cfg.Groups[0].Services = append(cfg.Groups[0].Services, domain.GroupServiceConfig{ID: name, DisplayName: name, RepositoryID: name, GenbuServiceName: name, GenbuProbeEnabled: true, MasterBranch: "origin/master", TestTagBaselineRef: "origin/release/test", DevelopmentTool: domain.DevelopmentToolVisualStudioCode, Modules: []domain.ServiceModuleConfig{{ID: "module", Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildCurrentBranch, TagEnabled: true}}})
		op.BatchID = &batch
		op.GenbuStatus = domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown, CheckedAt: &now}
		ops[name] = op
		saveTagProbeFixture(t, scope.TaskPath, op)
	}
	if e = a.Config.Save(cfg); e != nil {
		t.Fatal(e)
	}
	cfg, e = a.Config.Load()
	if e != nil {
		t.Fatal(e)
	}
	action := tagProbeFixtureAction(scope, "tag.notice.prepare", "")
	action.Payload["groupId"] = batch
	action.Payload["noticeConfigIdentity"] = ReleaseNoticeConfigIdentity(cfg)
	first, e := a.releaseNoticeBatch(context.Background(), action, true)
	if e != nil || len(first.rows) != 2 {
		t.Fatal(first, e)
	}
	op := ops["slow"]
	op.GenbuStatus.Build = domain.GenbuBuilding
	saveTagProbeFixture(t, scope.TaskPath, op)
	if _, e = a.releaseNoticeBatch(context.Background(), action, false); e == nil {
		t.Fatal("successful subset accepted")
	}
	saveTagProbeFixture(t, scope.TaskPath, ops["slow"])
	op = ops["slow"]
	tag := "changed.beta-2"
	op.Tag = &tag
	saveTagProbeFixture(t, scope.TaskPath, op)
	changed, e := a.releaseNoticeBatch(context.Background(), action, false)
	if e != nil || changed.scope == first.scope {
		t.Fatal("changed batch reused notice scope", e)
	}
	cfg.Groups[0].Services[0].GenbuProbeEnabled = false
	if e = a.Config.Save(cfg); e != nil {
		t.Fatal(e)
	}
	cfg, _ = a.Config.Load()
	action.Payload["noticeConfigIdentity"] = ReleaseNoticeConfigIdentity(cfg)
	if _, e = a.releaseNoticeBatch(context.Background(), action, false); e == nil || !strings.Contains(e.Error(), "探测") {
		t.Fatal(e)
	}
	action.Payload["noticeConfigIdentity"] = "old-config"
	if _, e = a.releaseNoticeBatch(context.Background(), action, false); e == nil {
		t.Fatal("stale config accepted")
	}
}

func TestReleaseNoticeConfigFieldsCanAddContactsWithoutPreexistingObject(t *testing.T) {
	cfg := domain.DefaultAppConfig()
	notice := cfg.EffectiveLarkNotice()
	notice.Contacts = append(notice.Contacts, domain.LarkNoticeContact{Name: "测试同事", OpenID: "ou_synthetic"})
	data, _ := json.Marshal(notice)
	next, e := mergeConfigurationFields(cfg, ConfigFieldsUpdate{Fields: map[string]json.RawMessage{"larkNotice": data}})
	if e != nil || len(next.EffectiveLarkNotice().Contacts) != 1 {
		t.Fatal(next.LarkNotice, e)
	}
	if cfg.LarkNotice != nil {
		t.Fatal("mutated original config")
	}
}

func TestReleaseNoticeProbeFreshnessDoesNotAcceptUnknownTime(t *testing.T) {
	now := domain.FormatSilverWingTime(time.Now())
	old := domain.FormatSilverWingTime(time.Now().Add(-2 * time.Minute))
	future := domain.FormatSilverWingTime(time.Now().Add(time.Hour))
	for _, v := range []struct {
		time  *string
		fresh bool
	}{{nil, false}, {&now, true}, {&old, false}, {&future, false}} {
		if got := releaseNoticeProbeFresh(domain.GenbuTagProbeStatus{CheckedAt: v.time}); got != v.fresh {
			t.Fatal(v, got)
		}
	}
}
