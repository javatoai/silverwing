package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func releaseNoticeFixture(t *testing.T, id string) (*app, tagHistoryGroup) {
	t.Helper()
	s := tagLayoutFixture(t)
	s.taskRequirementMetadata = map[string]taskRequirementMetadataState{
		"task-a": {URL: s.tasks[0].requirementURL, Status: requirementMetadataLoaded, Metadata: requirementMetadata{
			Participants: domain.RequirementParticipants{QCOwners: []domain.RequirementPerson{{Name: "测试甲"}, {Name: "测试乙"}, {Name: " 测试甲 "}, {Name: ""}}, ProductManagers: []domain.RequirementPerson{{Name: "产品角色不可当测试"}}},
		}},
	}
	for _, group := range filterTagHistory(s.taskTagHistoryRecords("task-a"), "", false) {
		if group.Item.GroupID == id {
			return s, group
		}
	}
	t.Fatalf("synthetic batch %q missing", id)
	return nil, tagHistoryGroup{}
}

func releaseNoticeTester(s *app, group tagHistoryGroup, width, height int, dark bool) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		ui.Column(c).Fill().Background(p.bg).Children(func() {
			label(c, "合成发版记录", 14, p.text)
			s.renderTagHistoryGroup(c, p, &tagHistoryViewState{TaskID: "task-a"}, group)
		})
		s.renderOperationNotifications(c)
	}, width, height)
}

func TestTagReleaseNoticeCopyFeedback(t *testing.T) {
	for _, id := range []string{"running", "batch"} {
		t.Run(id, func(t *testing.T) {
			s, group := releaseNoticeFixture(t, id)
			tester := releaseNoticeTester(s, group, 900, 700, false)
			text := s.tagHistoryAnnouncement(group)
			if err := tester.Click("复制发版通知 · " + id); err != nil {
				t.Fatal(err)
			}
			want := "已复制：\n\n" + text + "\n\n请发送给：测试甲、测试乙"
			if tester.Clipboard() != text || s.lastMessage != want || s.lastError != "" || !tester.HasText(want) {
				t.Fatalf("actual clipboard/feedback mismatch: clipboard=%q feedback=%q", tester.Clipboard(), s.lastMessage)
			}
			if err := tester.Click("关闭"); err != nil {
				t.Fatal(err)
			}
			if s.lastMessage != "" || tester.HasText(want) {
				t.Fatal("release toast close did not clear only its feedback")
			}
			if err := tester.Click("复制发版通知 · " + id); err != nil {
				t.Fatal(err)
			}
			for range 8 {
				if tester.Focused("关闭") {
					break
				}
				tester.Key(0, ui.KeyTab)
			}
			if !tester.Focused("关闭") {
				t.Fatal("toast close is not keyboard reachable")
			}
			tester.Key(0, ui.KeyEscape)
			if s.lastMessage != "" {
				t.Fatal("Escape did not retain native toast dismissal")
			}
		})
	}
}

func TestTagReleaseNoticeRecipientsAreTaskScoped(t *testing.T) {
	s, group := releaseNoticeFixture(t, "batch")
	text := "原始复制文本\r\n服务名 · 标签\n分支 feature/合成分支\n"
	message := s.tagHistoryAnnouncementCopiedMessage(group, text)
	if message != tagReleaseNoticeCopiedPrefix+text+tagReleaseNoticeRecipientSeparator+"测试甲、测试乙" {
		t.Fatal("feedback changed the actual clipboard bytes or duplicate-name order")
	}
	metadata := s.taskRequirementMetadata["task-a"]
	metadata.Metadata.Participants.QCOwners = nil
	s.taskRequirementMetadata["task-a"] = metadata
	if !strings.HasSuffix(s.tagHistoryAnnouncementCopiedMessage(group, text), "请发送给：未设置测试负责人") {
		t.Fatal("missing test owners guessed another role")
	}
	email := "qa@example.invalid"
	metadata.Metadata.Participants.QCOwners = []domain.RequirementPerson{{Email: &email}, {Email: &email}}
	s.taskRequirementMetadata["task-a"] = metadata
	if !strings.HasSuffix(s.tagHistoryAnnouncementCopiedMessage(group, text), "请发送给："+email) {
		t.Fatal("existing real-person email fallback or deduplication lost")
	}
	metadata.URL = "https://example.invalid/another-requirement"
	s.taskRequirementMetadata["task-a"] = metadata
	if !strings.HasSuffix(s.tagHistoryAnnouncementCopiedMessage(group, text), "请发送给：未设置测试负责人") {
		t.Fatal("metadata from another requirement URL leaked into recipients")
	}
	metadata.URL = s.tasks[0].requirementURL
	s.taskRequirementMetadata["task-a"] = metadata
	s.tasks = append([]task{{id: "task-b", title: "其它任务", requirementURL: metadata.URL}}, s.tasks...)
	s.taskRequirementMetadata["task-b"] = taskRequirementMetadataState{URL: metadata.URL, Status: requirementMetadataLoaded, Metadata: requirementMetadata{Participants: domain.RequirementParticipants{QCOwners: []domain.RequirementPerson{{Name: "其它任务测试"}}}}}
	if got := s.tagHistoryAnnouncementCopiedMessage(group, text); !strings.HasSuffix(got, "请发送给："+email) || strings.Contains(got, "其它任务测试") {
		t.Fatal("current task or same-URL task replaced the owning batch's recipients")
	}
	s.invalidateTaskRequirementIdentityMetadata()
	if !strings.HasSuffix(s.tagHistoryAnnouncementCopiedMessage(group, text), "请发送给：未设置测试负责人") {
		t.Fatal("account invalidation retained a previous account's recipients")
	}
}

type releaseNoticeClipboardBackend struct {
	text, attempted string
	calls           int
}

func (*releaseNoticeClipboardBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("fixture forbids snapshots")
}
func (*releaseNoticeClipboardBackend) Dispatch(context.Context, application.Action) (application.ActionResult, error) {
	return application.ActionResult{}, errors.New("fixture forbids business operations")
}
func (b *releaseNoticeClipboardBackend) ReadRichClipboard() (application.RichClipboardContent, error) {
	return application.RichClipboardContent{Text: b.text}, nil
}
func (b *releaseNoticeClipboardBackend) WriteRichClipboard(value application.RichClipboardContent) error {
	b.calls++
	b.attempted = value.Text
	return errors.New("synthetic clipboard locked")
}

func TestTagReleaseNoticeClipboardFailure(t *testing.T) {
	s, group := releaseNoticeFixture(t, "batch")
	backend := &releaseNoticeClipboardBackend{text: "原剪贴板"}
	s.backend = backend
	tester := releaseNoticeTester(s, group, 900, 700, false)
	if err := tester.Click("复制发版通知 · batch"); err != nil {
		t.Fatal(err)
	}
	if backend.calls != 1 || backend.text != "原剪贴板" || backend.attempted != s.tagHistoryAnnouncement(group) || s.lastMessage != "" || s.lastError != "复制失败：synthetic clipboard locked" || tester.HasText("请发送给：") {
		t.Fatal("failed copy changed the clipboard or produced a success/recipient toast")
	}
}

func TestTagReleaseNoticeToastOptIn(t *testing.T) {
	valid := tagReleaseNoticeCopiedPrefix + "合成发版全文" + tagReleaseNoticeRecipientSeparator + "测试甲"
	for _, test := range []struct {
		message, failure, scope string
		height                  float32
	}{
		{valid, "", "operation", 360},
		{valid, "", "other", 0},
		{"普通复制已完成", "", "operation", 0},
		{"普通多行成功\n完整内容", "", "operation", 0},
		{"前缀不匹配：" + valid, "", "operation", 0},
		{tagReleaseNoticeCopiedPrefix + "没有发送对象", "", "operation", 0},
		{tagReleaseNoticeCopiedPrefix + tagReleaseNoticeRecipientSeparator + "测试甲", "", "operation", 0},
		{tagReleaseNoticeCopiedPrefix + "内容" + tagReleaseNoticeRecipientSeparator + " ", "", "operation", 0},
		{valid, "复制失败", "operation", 0},
	} {
		t.Run(fmt.Sprintf("%s-%q-%q", test.scope, test.message, test.failure), func(t *testing.T) {
			var shown []ui.Toast
			state := operationNotificationState{}
			ui.NewTester(func(c *ui.Context) {
				setTheme(c, false)
				state.present(c, test.scope, &test.message, &test.failure)
				ui.ToastViewportBase(c, func(_ ui.Element, toasts []ui.Toast) { shown = append([]ui.Toast(nil), toasts...) })
			}, 600, 500)
			if len(shown) != 1 || shown[0].ContentMaxHeight != test.height || shown[0].Action != "关闭" {
				t.Fatalf("non-opt-in toast options changed: %+v", shown)
			}
			wantTimeout := 4 * time.Second
			if test.failure != "" {
				wantTimeout = -1
			}
			if shown[0].Timeout != wantTimeout {
				t.Fatal("existing toast lifecycle changed")
			}
		})
	}
}

func TestTagReleaseNoticeLongToast(t *testing.T) {
	for _, size := range [][2]int{{360, 320}, {1000, 700}} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d-dark-%t", size[0], size[1], dark), func(t *testing.T) {
				s, group := releaseNoticeFixture(t, "running")
				group.Item.Operations = nil
				for i := range 24 {
					tag := fmt.Sprintf("1.2.%d.beta-1", i)
					group.Item.Operations = append(group.Item.Operations, domain.TagOperation{OperationID: fmt.Sprintf("synthetic-%02d", i), ServiceName: strings.Repeat("synthetic-service-", 4), Tag: &tag, State: domain.TagOperationSuccess})
				}
				tester := releaseNoticeTester(s, group, size[0], size[1], dark)
				if err := tester.Click("复制发版通知 · running"); err != nil {
					t.Fatal(err)
				}
				message := s.lastMessage
				body, bodyFound := tester.Find(message)
				close, closeFound := tester.Find("关闭")
				if !bodyFound || !closeFound || close.X < 0 || close.X+close.W > float32(size[0]) || close.Y < 0 || close.Y+close.H > float32(size[1]) || body.X < 0 || body.X+body.W > close.X {
					t.Fatalf("long toast body or persistent close escaped the window: body=%+v close=%+v", body, close)
				}
				tester.Move(close.X+close.W/2, close.Y+close.H/2)
				tester.Frame()
				captureReleaseNotice(t, tester, fmt.Sprintf("release-%d-dark-%t-top", size[0], dark))
				beforePixels := append([]byte(nil), tester.Image().Pix...)
				tester.Scroll(body.X+body.W/2, close.Y+close.H/2, 0, 100000)
				tester.Frame()
				changed := false
				img := tester.Image()
				for y := int(body.Y) + 4; y < int(body.Y+body.H)-4; y++ {
					a := img.PixOffset(int(body.X)+4, y)
					z := img.PixOffset(int(body.X+body.W)-10, y)
					changed = changed || !bytes.Equal(beforePixels[a:z], img.Pix[a:z])
				}
				if !changed || tester.Clipboard() != s.tagHistoryAnnouncement(group) || !strings.HasSuffix(message, "请发送给：测试甲、测试乙") {
					t.Fatal("scrolling did not move the actual rendered text, or changed the full clipboard/recipient suffix")
				}
				captureReleaseNotice(t, tester, fmt.Sprintf("release-%d-dark-%t-bottom", size[0], dark))
				if err := tester.Click("关闭"); err != nil || s.lastMessage != "" {
					t.Fatalf("close must remain clickable after scrolling: %v", err)
				}
			})
		}
	}
}

func captureReleaseNotice(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("SILVERWING_RELEASE_NOTICE_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	encodeErr := png.Encode(file, tester.Image())
	if closeErr := file.Close(); encodeErr != nil || closeErr != nil {
		t.Fatalf("capture: %v / %v", encodeErr, closeErr)
	}
}
