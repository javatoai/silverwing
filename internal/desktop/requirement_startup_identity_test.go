package desktop

import (
	"testing"

	"silverwing-go/internal/application"
)

func TestRequirementStartupPayloadOmitsUnconfirmedIdentity(t *testing.T) {
	for _, kind := range []string{"requirements.read", "requirements.comments"} {
		s := &app{}
		reader := s.requirementReader("task", "https://project.feishu.cn/obt/technical/detail/1000000008")
		payload := s.requirementReaderPayload(reader, kind, false)
		if value, exists := payload["expectedIdentity"]; exists {
			t.Fatalf("startup %s sent an unconfirmed identity: %#v", kind, value)
		}
		s.requirementUnit.closeRequirementReaders()
	}
	s := &app{}
	if value, exists := s.bindRequirementAccount(map[string]any{"force": false})["expectedIdentity"]; exists {
		t.Fatalf("startup metadata sent an unconfirmed identity: %#v", value)
	}
}

func TestRequirementPayloadKeepsConfirmedIdentity(t *testing.T) {
	s := &app{
		requirementUnit: requirementController{state: requirementState{requirementIdentityJobs: &requirementIdentityJobState{confirmed: true, state: application.RequirementIdentityAuthenticated, identity: "confirmed-account-token"}}},
	}
	reader := s.requirementReader("task", "https://project.feishu.cn/obt/technical/detail/1000000008")
	payload := s.requirementReaderPayload(reader, "requirements.read", true)
	if payload["expectedIdentity"] != "confirmed-account-token" {
		t.Fatal("confirmed account binding was dropped")
	}
	s.requirementUnit.closeRequirementReaders()
}
