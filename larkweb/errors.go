package larkweb

import "fmt"

// Error is a gateway call that did not land. No field ever holds a cookie
// value: Reason is written here, and a jar failure in Err is kooky's, which
// names a row before its value is decrypted.
type Error struct {
	// Op names what was attempted, e.g. "mark read".
	Op string
	// HTTPStatus is the gateway's HTTP status, 0 when the request never got a
	// reply.
	HTTPStatus int
	// Status is the status inside the reply envelope, 0 when there was none.
	Status uint32
	Reason string
	Err    error
}

func (e *Error) Error() string {
	msg := e.Op
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	if e.HTTPStatus != 0 {
		msg += fmt.Sprintf(" (http %d)", e.HTTPStatus)
	}
	if e.Status != 0 {
		msg += fmt.Sprintf(" (status %d)", e.Status)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }
