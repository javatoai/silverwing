package desktop

import (
	"silverwing-go/internal/application"
)

func legacyOperation[T any](result application.OperationResult[T], err error) (application.ActionResult, error) {
	return result.ToActionResult(), err
}
