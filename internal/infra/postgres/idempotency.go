package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

// Запросы хранилища ключей.
//
// ON CONFLICT DO NOTHING, а не DO UPDATE: занятый ключ трогать нельзя — за
// ним стоит уже созданная задача, и переписать его значило бы потерять ответ,
// который клиент ещё не получил. Ноль затронутых строк и означает «занят»,
// а чем именно — выясняет следующий запрос.
const (
	insertKey = `INSERT INTO idempotency_keys (owner_id, key, fingerprint, task_id, created_at)
		VALUES (@owner_id, @key, @fingerprint, @task_id, now())
		ON CONFLICT (owner_id, key) DO NOTHING`

	selectKey = `SELECT fingerprint, task_id FROM idempotency_keys
		WHERE owner_id = @owner_id AND key = @key`
)

// IdempotencyStore — реализация app.IdempotencyStore поверх таблицы ключей.
//
// Живёт внутри транзакции: ключ записывается той же операцией, что и задача.
// Иначе повтор, пришедший между двумя записями, создал бы дубль — ровно то,
// от чего ключ и защищает.
type IdempotencyStore struct {
	db db
}

// Reserve закрепляет ключ за задачей или сообщает о повторе.
//
// Параллельный повтор ждёт на уникальном индексе, пока первый запрос не
// зафиксируется, и после этого читает уже записанный ответ. Замок здесь —
// сама база, и заводить второй в коде нечем и незачем.
func (s *IdempotencyStore) Reserve(ctx context.Context, key app.IdempotencyKey) (todo.TaskID, bool, error) {
	tag, err := s.db.Exec(ctx, insertKey, pgx.StrictNamedArgs{
		"owner_id":    key.OwnerID.String(),
		"key":         key.Key,
		"fingerprint": key.Fingerprint,
		"task_id":     key.TaskID.String(),
	})
	if err != nil {
		return todo.TaskID{}, false, fmt.Errorf("postgres: reserve idempotency key: %w", err)
	}

	// Ключ был свободен и достался этому запросу.
	if tag.RowsAffected() == 1 {
		return key.TaskID, false, nil
	}

	return s.stored(ctx, key)
}

// stored читает занятый ключ и решает, повтор это или другой запрос.
func (s *IdempotencyStore) stored(ctx context.Context, key app.IdempotencyKey) (todo.TaskID, bool, error) {
	var (
		fingerprint []byte
		storedID    string
	)
	err := s.db.QueryRow(ctx, selectKey, pgx.StrictNamedArgs{
		"owner_id": key.OwnerID.String(),
		"key":      key.Key,
	}).Scan(&fingerprint, &storedID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Строка была на месте мгновение назад — вставка на неё и наткнулась, —
		// а сейчас её нет. Уборка старых ключей или рука в psql; повторить
		// запрос безопасно, поэтому это конфликт, а не повтор.
		return todo.TaskID{}, false, fmt.Errorf("postgres: read idempotency key (vanished): %w",
			app.ErrIdempotencyKeyReused)
	case err != nil:
		return todo.TaskID{}, false, fmt.Errorf("postgres: read idempotency key: %w", err)
	}

	// Тот же ключ с другим содержимым — не повтор: клиент спросил о другом,
	// и отдавать ему прошлый ответ нельзя.
	if !bytes.Equal(fingerprint, key.Fingerprint) {
		return todo.TaskID{}, false, fmt.Errorf("postgres: reserve idempotency key (task %s): %w",
			storedID, app.ErrIdempotencyKeyReused)
	}

	taskID, err := todo.ParseTaskID(storedID)
	if err != nil {
		return todo.TaskID{}, false, fmt.Errorf("postgres: read idempotency key task id (%v): %w",
			err, errCorruptRow)
	}

	return taskID, true, nil
}
