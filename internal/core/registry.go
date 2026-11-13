package core

import (
	"context"
	"errors"
)

var ErrAuthorizationCancelled = errors.New("用户取消恢复授权")

// Registry 是唯一允许 PATH 写入的接口，测试使用独立存储，绝不写开发机真实 PATH。
type Registry interface {
	Read(context.Context) (State, error)
	Authorize(context.Context, []Scope) (func(), error)
	Write(context.Context, Scope, Value) error
	Notify(context.Context) error
}
