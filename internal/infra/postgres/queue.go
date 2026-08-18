package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deliseev/todoer/internal/infra/outbox"
)

// Запросы очереди со стороны доставщика.
//
// FOR UPDATE SKIP LOCKED — то, ради чего очередь живёт в базе: строки,
// взятые одним доставщиком, другой пропускает, а не ждёт на них. Без SKIP
// LOCKED второй доставщик стоял бы в очереди за теми же сообщениями и
// проснулся бы ровно для того, чтобы обнаружить их отправленными.
const (
	takeEvents = `SELECT id, aggregate_id, name, payload, occurred_at
		FROM outbox
		WHERE published_at IS NULL
		ORDER BY id
		LIMIT @limit
		FOR UPDATE SKIP LOCKED`

	markPublished = `UPDATE outbox SET published_at = now() WHERE id = ANY(@ids)`

	markFailed = `UPDATE outbox
		SET attempts = attempts + 1, last_error = @cause
		WHERE id = ANY(@ids)`
)

// Queue — реализация outbox.Queue поверх таблицы очереди.
type Queue struct {
	pool *pgxpool.Pool
}

// NewQueue собирает очередь доставщика поверх пула соединений.
func NewQueue(pool *pgxpool.Pool) *Queue {
	return &Queue{pool: pool}
}

// Take забирает порцию сообщений, отдаёт их доставке и отмечает результат.
//
// Всё это — одна транзакция, и иначе нельзя: между «взял» и «отметил» не
// должно быть окна, в котором сообщение видно другому доставщику. Отметка
// об успехе фиксируется вместе с изъятием, отметка о неудаче — вместо неё,
// оставляя сообщение в очереди.
//
// Пока идёт доставка, транзакция открыта и держит строки. Это осознанная
// цена: доставка ограничена idle_in_transaction_session_timeout сессии, и
// зависший получатель отвалится по нему, а не унесёт очередь с собой.
func (q *Queue) Take(
	ctx context.Context,
	limit int,
	deliver func(ctx context.Context, messages []outbox.Message) error,
) (int, error) {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin take events: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	messages, err := takeBatch(ctx, tx, limit)
	if err != nil {
		return 0, err
	}
	// Пустая очередь — обычное дело: доставщик просыпается и на побудку,
	// и по страховочному сроку. Дёргать ради этого получателя незачем.
	if len(messages) == 0 {
		return 0, nil
	}

	ids := messageIDs(messages)

	if deliverErr := deliver(ctx, messages); deliverErr != nil {
		// Причина запоминается в той же транзакции, что и отказ от отметки:
		// сообщения остаются в очереди, но очередь, которая не пустеет,
		// обязана уметь объяснить почему.
		if _, err := tx.Exec(ctx, markFailed, pgx.StrictNamedArgs{
			"ids": ids, "cause": deliverErr.Error(),
		}); err != nil {
			return 0, fmt.Errorf("postgres: mark events failed: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("postgres: commit failed events: %w", err)
		}

		return 0, deliverErr
	}

	if _, err := tx.Exec(ctx, markPublished, pgx.StrictNamedArgs{"ids": ids}); err != nil {
		return 0, fmt.Errorf("postgres: mark events published: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: commit published events: %w", err)
	}

	return len(messages), nil
}

// takeBatch забирает порцию сообщений под замком транзакции.
func takeBatch(ctx context.Context, tx pgx.Tx, limit int) ([]outbox.Message, error) {
	rows, err := tx.Query(ctx, takeEvents, pgx.StrictNamedArgs{"limit": limit})
	if err != nil {
		return nil, fmt.Errorf("postgres: take events: %w", err)
	}

	// Разбор по именам, как и у задачи: сместись список колонок относительно
	// структуры — будет ошибка о ненайденной колонке, а не тихо переставленные
	// значения.
	taken, err := pgx.CollectRows(rows, pgx.RowToStructByName[messageRow])
	if err != nil {
		return nil, fmt.Errorf("postgres: take events: %w", err)
	}

	messages := make([]outbox.Message, len(taken))
	for i, row := range taken {
		messages[i] = row.message()
	}

	return messages, nil
}

// messageIDs собирает номера сообщений для отметки.
func messageIDs(messages []outbox.Message) []int64 {
	ids := make([]int64, len(messages))
	for i, message := range messages {
		ids[i] = message.ID
	}
	return ids
}
