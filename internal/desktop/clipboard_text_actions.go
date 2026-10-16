package desktop

import (
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func (s *app) copyTextToClipboard(services ui.Services, text, message string) bool {
	if backend, ok := s.backend.(application.RichClipboardBackend); ok {
		if err := backend.WriteRichClipboard(application.RichClipboardContent{Text: text}); err != nil {
			s.lastMessage = ""
			s.lastError = "复制失败：" + err.Error()
			return false
		}
	} else {
		services.WriteClipboard(text)
	}
	s.lastMessage = message
	s.lastError = ""
	return true
}

func (s *app) clipboardTextWriter(services ui.Services) func(string) {
	return func(text string) { s.copyTextToClipboard(services, text, "") }
}
