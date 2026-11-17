package osupdate

import "errors"

// InputError is a request the user can fix. The message is safe to show.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is a failure while checking, staging, or applying an image.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

// BusyError means a slot write, rollback, or extension change is already running.
type BusyError struct{}

func (e *BusyError) Error() string {
	return "Another OS change is already running."
}

// ErrBusy is BusyError as a value for errors.Is.
var ErrBusy = &BusyError{}

func input(msg string) error { return &InputError{Msg: msg} }
func op(msg string) error    { return &OpError{Msg: msg} }

func isBusy(err error) bool { return errors.Is(err, ErrBusy) }
