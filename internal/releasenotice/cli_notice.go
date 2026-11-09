package releasenotice

import (
	"context"
	"errors"
)

type RecipientDelivery struct {
	Name      string `json:"name"`
	Self      bool   `json:"self"`
	State     string `json:"state"`
	Skipped   bool   `json:"skipped,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	Error     string `json:"error,omitempty"`
}

// SendCLI notifies only the verified current user and shares the desktop's receipt.
func (s Store) SendCLI(ctx context.Context, id string, g Gateway, validate func(context.Context, Session) error) ([]RecipientDelivery, error) {
	results := []RecipientDelivery{}
	_, err := s.withVerifiedSession(ctx, id, g, validate, func(out *Session) error {
		if out.Kind != "" {
			return errors.New("仅 Tag 发版通知支持此发送入口")
		}
		card, err := SessionCard(*out)
		if err != nil {
			return err
		}
		if err := validate(ctx, *out); err != nil {
			return err
		}
		receipt, skipped, err := s.sendReceipt(ctx, out, out.Identity.OpenID, card, Selection{Retry: true}, true, g)
		result := RecipientDelivery{Name: out.Identity.Name, Self: true, State: receipt.State, Skipped: skipped,
			MessageID: receipt.MessageID, Error: receipt.Error}
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
		if err != nil {
			return err
		}
		if receipt.State != "sent" {
			return errors.New("本人通知尚未确认成功，请核实或重试")
		}
		return nil
	})
	return results, err
}
