package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

func TestProductionCreateVersionContextAndVisibleErrors(t *testing.T) {
	for _, tc := range []struct {
		version   string
		wantError []string
	}{
		{version: "2.4.37", wantError: []string{"Tag 2.4.37", "分支 release/2.4.37", "release/2.4.38"}},
		{version: "2.4.38"},
		{version: "3.0.0"},
		{version: "2.5.0"},
		{version: "2.3.99", wantError: []string{"线上生产 Tag 2.4.36", "release/2.4.38"}},
	} {
		t.Run(tc.version, func(t *testing.T) {
			s, v, _ := productionFixture(t)
			s.win = &mygo.Window{}
			s.releaseUnit.state.production.batches = nil
			v.BatchID, v.ActiveStep = "", 3
			v.Preview.ProductionTag = "2.4.36"
			v.Preview.Tags = []string{"6.6.6", "2.4.37", "2.4.36"}
			v.Preview.Branches = []string{"master", "release/2.4.37"}
			v.Version = "release/" + tc.version
			s.confirmProductionCreate(v)
			if len(tc.wantError) == 0 {
				if s.confirmationDialog == nil || v.Error != "" || !strings.Contains(s.confirmationDialog.Detail, v.Version) {
					t.Fatalf("valid version did not reach confirmation: %s", v.Error)
				}
				return
			}
			if s.confirmationDialog != nil {
				t.Fatal("invalid version reached write confirmation")
			}
			for _, part := range tc.wantError {
				if !strings.Contains(v.Error, part) {
					t.Errorf("visible error %q does not include %q", v.Error, part)
				}
			}
			if !productionIterationTester(s, 960, false).HasText(v.Error) {
				t.Fatal("version validation error was not rendered in the release step")
			}
		})
	}
}
