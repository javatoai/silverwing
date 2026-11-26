package desktop

import (
	"cmp"
	"slices"
	"strings"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
)

const tagReleaseNoticeCopiedPrefix = "已复制：\n\n"
const tagReleaseNoticeRecipientSeparator = "\n\n请发送给："

func (s *app) tagReleaseNoticeCopy(taskID string, group tagHistoryGroup) (text, feedback string) {
	operations := slices.Clone(group.Item.Operations)
	slices.SortStableFunc(operations, func(a, b domain.TagOperation) int {
		if order := cmp.Compare(a.CreatedAt, b.CreatedAt); order != 0 {
			return order
		}
		return cmp.Compare(a.OperationID, b.OperationID)
	})

	item := s.taskUnit.taskByID(taskID)
	metadata := s.taskRequirementMetadataFor(item).Metadata
	var cachedTitle string
	if metadata.Title != nil {
		cachedTitle = *metadata.Title
	}
	title := firstNonEmpty(cachedTitle, item.requirementTitle, item.title, group.Item.FolderName, "未命名需求")
	text = delivery.FormatTagAnnouncement(title, operations, true)
	if !s.integrationReady("genbu") && tagReleaseNoticeTagsPushed(operations) {
		heading := "需求：" + strings.TrimSpace(title) + "\n"
		text = strings.Replace(text, heading+"测试Tag已构建完毕，辛苦发版：", heading+"测试Tag已推送，辛苦发版：", 1)
	}

	var names []string
	seen := make(map[string]bool)
	for _, name := range requirementPeopleNames(metadata.Participants.QCOwners) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	recipients := firstNonEmpty(strings.Join(names, "、"), "未设置测试负责人")
	return text, tagReleaseNoticeCopiedPrefix + text + tagReleaseNoticeRecipientSeparator + recipients
}

func tagReleaseNoticeTagsPushed(operations []domain.TagOperation) bool {
	for _, op := range operations {
		if op.State != domain.TagOperationSuccess || op.Tag == nil || strings.TrimSpace(*op.Tag) == "" {
			return false
		}
	}
	return len(operations) > 0
}

func isTagReleaseNoticeCopiedMessage(message string) bool {
	if !strings.HasPrefix(message, tagReleaseNoticeCopiedPrefix) {
		return false
	}
	separator := strings.LastIndex(message, tagReleaseNoticeRecipientSeparator)
	if separator <= len(tagReleaseNoticeCopiedPrefix) {
		return false
	}
	recipients := message[separator+len(tagReleaseNoticeRecipientSeparator):]
	return recipients != "" && strings.TrimSpace(recipients) == recipients
}
