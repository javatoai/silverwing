package commandruntime

import "context"

type admissionKey struct{}

// AdmissionError means the command was stopped before it could start.
type AdmissionError struct{ cause error }

func (e *AdmissionError) Error() string { return e.cause.Error() }
func (e *AdmissionError) Unwrap() error { return e.cause }
func (*AdmissionError) RequestNotSent() {}

// WithAdmission carries the admission check for each command in a workflow.
func WithAdmission(ctx context.Context, check func(context.Context) error) context.Context {
	return context.WithValue(ctx, admissionKey{}, check)
}

func CheckAdmission(ctx context.Context) error {
	if check, ok := ctx.Value(admissionKey{}).(func(context.Context) error); ok {
		if err := check(ctx); err != nil {
			return &AdmissionError{cause: err}
		}
	}
	return nil
}
