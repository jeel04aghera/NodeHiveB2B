package httpapi

import (
	"context"

	"github.com/nodehive/gpu-platform/internal/domain"
)

type contextWithUser struct {
	context.Context
	user domain.User
}

func (c contextWithUser) Value(key any) any {
	if key == (ctxUserKey{}) {
		return c.user
	}
	return c.Context.Value(key)
}

func withUser(ctx context.Context, u domain.User) context.Context {
	return contextWithUser{ctx, u}
}
