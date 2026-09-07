package query

import (
	"context"

	"github.com/andocodes/cassie/internal/domain/runtime"
	"github.com/andocodes/cassie/internal/ports"
)

type RecentSessions struct {
	Store ports.SessionStore
}

func (q RecentSessions) Handle(ctx context.Context, limit int) ([]runtime.Session, error) {
	return q.Store.Recent(ctx, limit)
}
