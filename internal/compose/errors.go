package compose

import "fmt"

// InputError is a validation failure whose message is safe to show.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is an operational failure whose message is safe to show.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

func inputf(format string, a ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, a...)}
}

func opf(format string, a ...any) error {
	return &OpError{Msg: fmt.Sprintf(format, a...)}
}
