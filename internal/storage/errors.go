package storage

import "fmt"

// InputError is a request FELDCORE will not carry out. The disk is unchanged.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is a command that was attempted and failed.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

func input(msg string) error { return &InputError{Msg: msg} }

func inputf(format string, args ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, args...)}
}

func op(msg string) error { return &OpError{Msg: msg} }

func opf(format string, args ...any) error {
	return &OpError{Msg: fmt.Sprintf(format, args...)}
}
