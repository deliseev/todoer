package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deliseev/todoer/internal/app"
)

// db объединяет пул и транзакцию, чтобы репозиторий мог работать
// как в изолированном чтении, так и внутри Unit of Work.
//
// Ради него хранилища и параметризованы: SQL у работы внутри транзакции и вне
// её один и тот же, и раздваивать его значило бы завести две версии каждого
// запроса, вторая из которых однажды отстанет от первой.
type db interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Статические проверки совместимости типов:
var (
	_ db = (*pgxpool.Pool)(nil)
	_ db = (pgx.Tx)(nil)
)

// UnitOfWork — реализация app.UnitOfWork поверх транзакции Postgres.
type UnitOfWork struct {
	pool *pgxpool.Pool
}

// NewUnitOfWork собирает единицу работы поверх пула соединений.
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork {
	return &UnitOfWork{pool: pool}
}

// Do выполняет работу в одной транзакции и фиксирует её результат.
func (u *UnitOfWork) Do(ctx context.Context, work func(ctx context.Context, tx app.Tx) error) error {
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin work: %w", err)
	}

	// Откат в defer, а не в ветке ошибки: он обязан случиться на любом выходе,
	// включая панику, — незавершённая работа не становится завершённой оттого,
	// что вышла необычным путём. После успешной фиксации откат безвреден:
	// транзакция уже закрыта, и pgx отвечает ErrTxClosed.
	//
	// Контекст берётся без отмены: отменённый запрос — самая частая причина
	// сюда попасть, и откатывать надо именно тогда. С отменённым контекстом
	// команда не ушла бы в базу вовсе, а соединение закрылось бы вместо этого.
	defer tx.Rollback(context.WithoutCancel(ctx))

	if err := work(ctx, txScope{tx: tx}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit work: %w", err)
	}

	return nil
}

// txScope — доступ к хранилищам внутри одной транзакции.
type txScope struct {
	tx pgx.Tx
}

// Tasks отдаёт хранилище задач, работающее в этой транзакции.
func (s txScope) Tasks() app.Repository {
	return &TaskRepository{db: s.tx}
}

// Outbox отдаёт исходящую очередь событий этой же транзакции.
func (s txScope) Outbox() app.Outbox {
	return &Outbox{db: s.tx}
}

// Keys отдаёт хранилище ключей идемпотентности этой же транзакции.
func (s txScope) Keys() app.IdempotencyStore {
	return &IdempotencyStore{db: s.tx}
}
