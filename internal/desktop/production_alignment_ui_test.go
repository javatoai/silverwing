package desktop

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

func productionAlignmentRect(t *testing.T, tester *ui.Tester, name string) ui.Rect {
	t.Helper()
	r, ok := tester.Find(name)
	if !ok || r.W <= 0 || r.H <= 0 {
		t.Fatalf("missing visible layout element %q: %+v", name, r)
	}
	return r
}

func TestProductionInformationColumnsAndStatusAlignment(t *testing.T) {
	for _, width := range []int{800, 960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, v, b := productionFixture(t)
				b = productionConflictBatch(b, "feature")
				b.Intent.SourceBranch = "feature/" + strings.Repeat("long-branch-name-", 10)
				s.production.batches[0] = b
				tester := productionCardsTester(s, width, dark)
				baseline := productionAlignmentRect(t, tester, "production-baseline-main值")
				for _, key := range []string{"production-baseline-tag", "production-baseline-main", "production-conflict-来源", "production-conflict-目标"} {
					name := productionAlignmentRect(t, tester, key+"标签")
					value := productionAlignmentRect(t, tester, key+"值")
					if math.Abs(float64(value.X-baseline.X)) > 1 || math.Abs(float64(name.Y-value.Y)) > 1 || value.X+value.W > float32(width) {
						t.Fatalf("information lost its shared column or first-line alignment: %s label=%+v value=%+v baseline=%+v", key, name, value, baseline)
					}
				}
				release := productionAlignmentRect(t, tester, "production-release-result值")
				releaseCard := productionAlignmentRect(t, tester, "创建 release 分支卡片")
				if release.X != releaseCard.X+17 || release.X+release.W > releaseCard.X+releaseCard.W-17 {
					t.Fatalf("release branch is not aligned with its card content: %+v %+v", release, releaseCard)
				}
				first := productionAlignmentRect(t, tester, productionInitializationCardTitles[0]+"状态")
				for _, title := range productionInitializationCardTitles {
					status := productionAlignmentRect(t, tester, title+"状态")
					heading := productionAlignmentRect(t, tester, title+"标题行")
					if math.Abs(float64(status.X+status.W-first.X-first.W)) > 1 || math.Abs(float64(status.Y+status.H/2-(heading.Y+heading.H/2))) > 1 {
						t.Fatalf("step status not aligned: step=%s status=%+v heading=%+v", title, status, heading)
					}
				}
				captureProductionSummary(t, tester, fmt.Sprintf("alignment-conflict-%d-dark-%t", width, dark))
				b.Intent, b.Phase = nil, "READY"
				s.production.batches[0] = b
				tester.Frame()
				picker := productionAlignmentRect(t, tester, "待合并的单个远端分支")
				card := productionAlignmentRect(t, tester, "合并分支卡片")
				if picker.X != card.X+17 || v.Branch != "feature/synthetic" || tester.HasText("合入到") {
					t.Fatal("returning to the branch picker lost alignment or selection, or restored the removed target row")
				}
			})
		}
	}
}

func TestProductionSyncAndRecordActionsStayAligned(t *testing.T) {
	for _, width := range []int{800, 960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, v, b := productionFixture(t)
				v.BuildChecked, v.Preview.MainInRelease = true, false
				b.Tags = []production.Tag{
					{Name: "2.3.44", Status: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess}},
					{Name: "2.3.44.1", PushedAt: "2026-10-23T00:00:00Z", Status: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding}},
				}
				b.Target.ServiceName = "service-" + strings.Repeat("long-name-", 8)
				v.Preview.Target = b.Target
				s.production.batches[0] = b
				tester := productionCardsTester(s, width, dark)
				tester.SetSize(width, 1800)
				tester.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
				card := productionAlignmentRect(t, tester, "构建生产 Tag卡片")
				diff := productionAlignmentRect(t, tester, "查看主分支差异")
				merge := productionAlignmentRect(t, tester, "合并主分支到 release")
				if diff.Y != merge.Y || diff.H != merge.H || math.Abs(float64(merge.X+merge.W-(card.X+card.W-17))) > 1 {
					t.Fatalf("sync action group is fragmented or not right aligned: diff=%+v merge=%+v card=%+v", diff, merge, card)
				}
				for _, tag := range b.Tags {
					copy := productionAlignmentRect(t, tester, "复制 Tag 记录 "+tag.Name)
					status := productionAlignmentRect(t, tester, "Tag 构建状态 "+tag.Name)
					name := productionAlignmentRect(t, tester, "Tag 服务与版本 "+tag.Name)
					timestamp := productionAlignmentRect(t, tester, "Tag 推送时间 "+tag.Name)
					if math.Abs(float64(copy.X-status.X-status.W-8)) > 1 || math.Abs(float64(copy.Y+copy.H/2-status.Y-status.H/2)) > 1 ||
						copy.X+copy.W > card.X+card.W-17 || name.X+name.W > card.X+card.W-17 ||
						timestamp.Y > name.Y+name.H/2 || status.Y+status.H/2 < name.Y+name.H/2-1 {
						t.Fatalf("Tag record lost reading order, adjacent actions, or wrapping: time=%+v name=%+v status=%+v copy=%+v", timestamp, name, status, copy)
					}
				}
				if !tester.HasText("时间未知") {
					t.Fatal("missing unknown timestamp fallback")
				}
				if err := tester.Click("复制 Tag 记录 2.3.44.1"); err != nil || tester.Clipboard() != productionTagRecordText(b, b.Tags[1]) {
					t.Fatalf("aligned copy action lost complete text: %v %q", err, tester.Clipboard())
				}
				captureProductionSummary(t, tester, fmt.Sprintf("alignment-records-%d-dark-%t", width, dark))
				b.Target.ServiceName = "data-sync-service"
				v.Preview.Target = b.Target
				s.production.batches[0] = b
				tester.Frame()
				if width >= 960 {
					var previous ui.Rect
					for _, key := range []string{"Tag 推送时间 2.3.44.1", "Tag 服务与版本 2.3.44.1", "Tag 构建状态 2.3.44.1", "复制 Tag 记录 2.3.44.1"} {
						item := productionAlignmentRect(t, tester, key)
						if previous.W > 0 && (math.Abs(float64(item.X-previous.X-previous.W-8)) > 1 || math.Abs(float64(item.Y+item.H/2-previous.Y-previous.H/2)) > 1) {
							t.Fatalf("short Tag record is not a compact single line: previous=%+v item=%+v", previous, item)
						}
						previous = item
					}
				}
				captureProductionSummary(t, tester, fmt.Sprintf("compact-tag-records-%d-dark-%t", width, dark))
			})
		}
	}
}
