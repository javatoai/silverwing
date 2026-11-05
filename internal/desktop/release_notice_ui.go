package desktop

import "silverwing-go/internal/releasenotice"

func (s *app) noticeButtonText(kind, groupID string) string {
	text := "发送给自己"
	d := s.releaseNotices.pending
	if d != nil && d.kind == kind && d.groupID == groupID && s.releaseNoticeCurrent(d) {
		if d.loading {
			return "准备中…"
		}
		if d.sending {
			return "发送中…"
		}
		if d.session != nil {
			if r, ok := d.session.LatestReceipt(); ok {
				if r.State == "sent" {
					text = "再次发送给自己"
				} else {
					text = "重试发送给自己"
				}
			}
		}
	}
	if kind == releasenotice.Branches {
		if text == "发送给自己" {
			return "发送分支通知给自己"
		}
		if text == "再次发送给自己" {
			return "再次发送分支通知给自己"
		}
		if text == "重试发送给自己" {
			return "重试发送分支通知给自己"
		}
	}
	return text
}
