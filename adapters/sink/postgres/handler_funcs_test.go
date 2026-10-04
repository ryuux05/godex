package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerFunctionsPreserveArgumentsAndErrors(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "value")
	expected := errors.New("handler failure")
	ev := types.Event{Id: "event", ChainId: "1"}
	handled, rolled := false, false
	h := HandlerFuncs{
		HandleEvent: func(got context.Context, tx pgx.Tx, event types.Event) error {
			handled = true
			assert.Same(t, ctx, got)
			assert.Nil(t, tx)
			assert.Equal(t, ev, event)
			return expected
		},
		RollbackEvents: func(got context.Context, tx pgx.Tx, chain string, from uint64) error {
			rolled = true
			assert.Same(t, ctx, got)
			assert.Nil(t, tx)
			assert.Equal(t, "1", chain)
			assert.Equal(t, uint64(7), from)
			return expected
		},
	}
	require.NoError(t, h.Validate())
	assert.ErrorIs(t, h.Handle(ctx, nil, ev), expected)
	assert.ErrorIs(t, h.Rollback(ctx, nil, "1", 7), expected)
	assert.True(t, handled)
	assert.True(t, rolled)
	var nilFunc HandlerFunc
	assert.Error(t, nilFunc.Handle(ctx, nil, ev))
}

func TestIncompleteHandlerFunctionsFailBeforeDatabaseAccess(t *testing.T) {
	for _, h := range []HandlerFuncs{{}, {HandleEvent: func(context.Context, pgx.Tx, types.Event) error { return nil }}, {RollbackEvents: func(context.Context, pgx.Tx, string, uint64) error { return nil }}} {
		_, err := NewSink(SinkConfig{Pool: &pgxpool.Pool{}, Handler: h})
		assert.ErrorContains(t, err, "HandleEvent and RollbackEvents")
		assert.Error(t, h.Handle(context.Background(), nil, types.Event{}))
		assert.Error(t, h.Rollback(context.Background(), nil, "1", 0))
	}
}
