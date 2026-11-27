package desktop

import (
	"fmt"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
)

func (s *app) checkProductionReleaseBranch(v *productionView) {
	if !productionMainIncluded(v.Preview) {
		return
	}
	input := strings.TrimSpace(v.Version)
	suggested := v.SuggestedBranch
	if suggested == "" && v.Preview.SuggestedVersion != "" {
		suggested = "release/" + v.Preview.SuggestedVersion
	}
	if input == suggested {
		s.confirmProductionCreate(v)
		return
	}
	version, err := production.ParseReleaseBranch(input)
	if err != nil {
		v.Error = err.Error()
		return
	}
	s.productionRequestFor(v, application.Action{Kind: "production.release-preview", ServiceID: v.Selection.ServiceID,
		Payload: map[string]any{"batchId": v.BatchID, "preview": *v.Preview, "version": version, "allowReuse": true}}, "")
}

func (s *app) productionReleaseInputCurrent(v *productionView, id, version string) bool {
	if v.BatchID != id || strings.TrimSpace(v.Version) != "release/"+version {
		return false
	}
	batch := s.productionBatch(v)
	if batch == nil {
		return id == ""
	}
	return batch.ID == id && !batch.Ended && batch.Intent == nil && batch.InitialSHA == "" &&
		v.Preview != nil && batch.Target == v.Preview.Target
}

func productionReleaseBaselineMatches(current *production.Preview, checked production.Preview) bool {
	return productionMainIncluded(current) && current.Target == checked.Target && current.ProductionTag == checked.ProductionTag &&
		current.ProductionSHA == checked.ProductionSHA && current.MainSHA == checked.MainSHA
}

func (s *app) confirmProductionReleasePlan(v *productionView, plan production.ReleaseBranchPlan) {
	baseline, id, p := v.Preview, v.BatchID, plan.Preview
	current := func() bool {
		return v.Preview == baseline && productionReleaseBaselineMatches(v.Preview, p) &&
			s.productionReleaseInputCurrent(v, id, plan.Version)
	}
	if p.ReleaseSHA == "" {
		s.confirmProductionNewRelease(v, p, plan.Version, current)
		return
	}
	branch := "release/" + plan.Version
	detail := productionTargetDetail(p.Target) + fmt.Sprintf("\n分支：%s\n远端提交：%s", branch, p.ReleaseSHA)
	s.confirmProduction(v, p.Target, productionConfirmationContent{
		Title: "远端分支已存在", ConfirmLabel: "复用分支", Destination: p.Target.Remote + "/" + branch,
		Detail: detail, Notices: []string{"当前批次将从远端提交 " + shortProductionSHA(p.ReleaseSHA) + " 继续。"},
		Current: current,
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.reuse-release", ServiceID: v.Selection.ServiceID,
			Payload: map[string]any{"batchId": id, "preview": p, "version": plan.Version, "confirmed": true}}, "")
	})
}
