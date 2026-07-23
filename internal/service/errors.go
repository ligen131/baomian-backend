package service

import "fmt"

type Error struct {
	Code    string
	Message string
	Details map[string]any
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

func NewError(code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}
