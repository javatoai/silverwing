package workspace

import (
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/productprofile"
)

func TestMigrationJournalProfileConsumerPreservesStrictEditionAndRefusal(t *testing.T) {
	wantError, ok := map[string]string{
		"qa":      "迁移日志不是 QA 格式",
		"product": "迁移日志不是产品版格式",
	}[productprofile.Edition]
	if !ok {
		t.Fatalf("restricted migration consumer used in edition %q", productprofile.Edition)
	}
	service := &TaskRootMigrationService{}
	for _, edition := range []string{"", "qa", "product", "QA", " product"} {
		t.Run("edition="+edition, func(t *testing.T) {
			manifest := domain.DefaultTaskManifest()
			manifest.Edition = edition
			task := migrationJournalTask{OriginalManifest: manifest}
			_, identityErr := migrationWorkspaceIdentities(task)
			_, recoveryErr := service.recoveryRepositoryLocks(migrationJournal{Tasks: []migrationJournalTask{task}})
			for name, err := range map[string]error{"identity": identityErr, "recovery": recoveryErr} {
				if edition == productprofile.Edition {
					if err != nil {
						t.Fatalf("%s rejected the current journal: %v", name, err)
					}
				} else if err == nil || err.Error() != wantError {
					t.Fatalf("%s foreign journal refusal = %v, want exact bytes %q", name, err, wantError)
				}
			}
		})
	}
}
