package rpcinterceptor

import "fmt"

func Errorf(code uint32, format string, args ...interface{}) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

type Error struct {
	Code uint32
	Msg  string
}

func (e *Error) Error() string { return e.Msg }
