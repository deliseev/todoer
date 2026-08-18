package postgres

import (
	"context"
	"fmt"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// outboxChannel — канал уведомлений о пополнении очереди.
//
// Имя с префиксом приложения: каналы живут в пределах базы и общие для всех,
// кто к ней подключён.
const outboxChannel = "todoer_outbox"

// Запросы очереди.
//
// NOTIFY идёт без полезной нагрузки, и это решение, а не экономия. Данные
// лежат в строке, а уведомление говорит только «в очереди что-то есть»:
// потерять с ним нечего, потолок Postgres в 8000 байт не мешает, а
// схлопывание одинаковых уведомлений внутри транзакции работает в нашу
// пользу — на любое число событий доставщик будится один раз.
const (
	insertEvent = `INSERT INTO outbox (aggregate_id, name, payload, occurred_at)
		VALUES (@aggregate_id, @name, @payload, @occurred_at)`

	notifyOutbox = `NOTIFY ` + outboxChannel
)

// Outbox — реализация app.Outbox поверх таблицы очереди.
//
// Живёт внутри транзакции: события ложатся рядом с задачей и становятся
// видимыми ровно тогда, когда становится видимой она.
type Outbox struct {
	db db
}

// Add кладёт события в очередь и будит доставщика.
func (o *Outbox) Add(ctx context.Context, events []todo.DomainEvent) error {
	// Пустая партия — корректный вход: решать, что считать изменением, не
	// дело очереди. Заодно не будим доставщика впустую.
	if len(events) == 0 {
		return nil
	}

	// Вставка по одной, а не пачкой: за одну работу домен порождает единицы
	// событий — их столько, сколько мутаций в команде, — и городить ради
	// этого пакетную отправку значило бы усложнять то, что не жмёт.
	for _, event := range events {
		row, err := newOutboxRow(event)
		if err != nil {
			return err
		}

		if _, err := o.db.Exec(ctx, insertEvent, row.args()); err != nil {
			return fmt.Errorf("postgres: add event %s of task %s: %w",
				event.EventName(), event.AggregateID(), err)
		}
	}

	// Уведомление уходит только при фиксации транзакции — Postgres придерживает
	// его до COMMIT. Поэтому «событие записано» и «доставщика позвали» —
	// один факт: разбудить его раньше времени физически нечем.
	if _, err := o.db.Exec(ctx, notifyOutbox); err != nil {
		return fmt.Errorf("postgres: notify %s: %w", outboxChannel, err)
	}

	return nil
}
