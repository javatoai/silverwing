package production

import (
	"context"
	"errors"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/genbuapi"
)

var (
	ErrUATPodPreflightStale = genbuapi.ErrUATPodPreviewStale
	ErrUATPodSubmitUnknown  = genbuapi.ErrUATPodSubmitUnknown
	ErrUATPodSubmitRejected = genbuapi.ErrUATPodSubmitRejected
)

func (g NativeGenbu) PreviewUATPodOperation(ctx context.Context, cfg domain.AppConfig, request genbuapi.UATPodOperationRequest) (genbuapi.UATPodOperationResult, error) {
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return genbuapi.UATPodOperationResult{}, err
	}
	result, err := client.PreviewUATPodOperation(ctx, request)
	if scopeErr := checkGenbuReadScope(ctx); scopeErr != nil {
		return genbuapi.UATPodOperationResult{}, scopeErr
	}
	if err != nil {
		return genbuapi.UATPodOperationResult{}, genbuReadError(ctx, cfg, client, err)
	}
	return result, nil
}

func (g NativeGenbu) SubmitUATPodOperation(ctx context.Context, cfg domain.AppConfig, request genbuapi.UATPodOperationRequest, previewID string, beforeWrite func(context.Context, genbuapi.UATPodOperationResult) error) (genbuapi.UATPodOperationResult, error) {
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return genbuapi.UATPodOperationResult{}, err
	}
	result, err := client.SubmitUATPodOperation(ctx, request, previewID, beforeWrite)
	var notSent genbuapi.RequestNotSentError
	switch {
	case errors.As(err, &notSent):
		return result, notSent
	case errors.Is(err, ErrUATPodSubmitUnknown), errors.Is(err, ErrUATPodSubmitRejected), errors.Is(err, ErrUATPodPreflightStale):
		return result, err
	case err != nil:
		return result, genbuReadError(ctx, cfg, client, err)
	default:
		return result, nil
	}
}
