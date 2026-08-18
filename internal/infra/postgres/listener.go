package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Listener — реализация outbox.Waiter поверх LISTEN/NOTIFY.
//
// Соединение своё, мимо пула, и это не прихоть: пул закрывает соединения по
// MaxConnIdleTime и MaxConnLifetime, а закрытое соединение молча теряет
// LISTEN — доставщик остался бы ждать уведомлений, которые ему больше никто
// не пришлёт. Заодно на нём нет сроков боевого пула: ожидание уведомления —
// не запрос, и statement_timeout ему ни к чему.
type Listener struct {
	dsn string

	mu   sync.Mutex
	conn *pgx.Conn
}

// NewListener собирает слушателя. Соединение он откроет при первом ожидании:
// поднимать его в конструкторе значило бы делать запуск приложения зависимым
// от того, что к делу ещё не приступало.
func NewListener(dsn string) *Listener {
	return &Listener{dsn: dsn}
}

// Wait ждёт уведомления о пополнении очереди, но не дольше timeout.
//
// Истёкший срок — не ошибка, а страховочный опрос: уведомление, случившееся
// в дыре между разрывом связи и повторным LISTEN, не повторится, и без опроса
// «не потерять» держалось бы на нём одном.
//
// Испорченное соединение закрывается, чтобы следующее ожидание начиналось с
// подключения. После него доставщик всё равно первым делом выгребает очередь,
// поэтому пропущенные за это время уведомления ничего не стоят.
func (l *Listener) Wait(ctx context.Context, timeout time.Duration) error {
	conn, subscribed, err := l.listen(ctx)
	if err != nil {
		return err
	}

	// Только что созданная подписка не ждёт ни секунды, и это не мелочь.
	// Подписка возникает здесь, то есть уже после того, как доставщик выгреб
	// очередь, — и всё, о чём уведомили в этом промежутке, не повторится.
	// Вернувшись сразу, мы заставляем его сходить в очередь ещё раз, теперь
	// уже подписанным: окно закрывается, а ждёт он на следующем заходе.
	if subscribed {
		return nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if _, err := conn.WaitForNotification(waitCtx); err != nil {
		switch {
		case ctx.Err() != nil:
			// Остановка приложения: соединение закроет Close.
			return ctx.Err()
		case errors.Is(err, context.DeadlineExceeded):
			// Срок ожидания вышел — пора проверить очередь. Соединение
			// это переживает: для pgx таймаут не фатален, в отличие от
			// всякой другой ошибки чтения.
			return nil
		}

		l.drop(ctx)

		return fmt.Errorf("postgres: wait for %s: %w", outboxChannel, err)
	}

	return nil
}

// Close закрывает соединение слушателя.
func (l *Listener) Close(ctx context.Context) {
	l.drop(ctx)
}

// listen отдаёт соединение, на котором уже выполнен LISTEN, открывая его при
// необходимости. Второе значение сообщает, что подписка создана прямо сейчас.
func (l *Listener) listen(ctx context.Context) (conn *pgx.Conn, subscribed bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.conn != nil && !l.conn.IsClosed() {
		return l.conn, false, nil
	}

	config, err := parseConfig(l.dsn)
	if err != nil {
		return nil, false, err
	}

	conn, err = pgx.ConnectConfig(ctx, config.ConnConfig)
	if err != nil {
		return nil, false, fmt.Errorf("postgres: connect listener: %w", err)
	}

	// LISTEN повторяется на каждом новом соединении: подписка живёт в сессии
	// и вместе с ней умирает.
	if _, err := conn.Exec(ctx, "LISTEN "+outboxChannel); err != nil {
		conn.Close(ctx)
		return nil, false, fmt.Errorf("postgres: listen %s: %w", outboxChannel, err)
	}

	l.conn = conn

	return conn, true, nil
}

// drop закрывает соединение, если оно есть.
func (l *Listener) drop(ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.conn == nil {
		return
	}

	// Контекст без отмены: закрывать соединение надо и тогда, когда сюда
	// привела именно отмена, — иначе прощание с базой не уедет.
	l.conn.Close(context.WithoutCancel(ctx))
	l.conn = nil
}
