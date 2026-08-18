package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/infra/outbox"
)

// Реализации обязаны удовлетворять портам доставщика.
var _ outbox.Publisher = outbox.NopPublisher{}

// waitTimeout — сколько тест ждёт результата фоновой работы. Щедро: он
// проверяет, что событие происходит, а не как быстро.
const waitTimeout = 2 * time.Second

func TestRelayDrainsQueue(t *testing.T) {
	t.Parallel()

	t.Run("очередь выгребается до дна", func(t *testing.T) {
		t.Parallel()

		// Больше одной порции: доставщик обязан вернуться за остатком, а не
		// уснуть на первой сотне.
		queue := newFakeQueue(250)
		publisher := &fakePublisher{}

		runRelay(t, queue, publisher, newFakeWaiter())

		if pending := queue.pendingCount(); pending != 0 {
			t.Errorf("в очереди осталось %d сообщений, ожидалось 0", pending)
		}
		if published := publisher.count(); published != 250 {
			t.Errorf("доставлено %d сообщений, ожидалось 250", published)
		}
	})

	t.Run("сообщения уезжают в порядке очереди", func(t *testing.T) {
		t.Parallel()

		queue := newFakeQueue(5)
		publisher := &fakePublisher{}

		runRelay(t, queue, publisher, newFakeWaiter())

		for i, id := range publisher.ids() {
			if want := int64(i + 1); id != want {
				t.Fatalf("сообщения доставлены в порядке %v", publisher.ids())
			}
		}
	})

	t.Run("пустая очередь публикатора не дёргает", func(t *testing.T) {
		t.Parallel()

		queue := newFakeQueue(0)
		publisher := &fakePublisher{}

		runRelay(t, queue, publisher, newFakeWaiter())

		if publisher.calls() != 0 {
			t.Errorf("публикатор вызван %d раз на пустой очереди", publisher.calls())
		}
	})
}

func TestRelayKeepsUndeliveredMessages(t *testing.T) {
	t.Parallel()

	// Отказ доставки — не потеря: сообщение остаётся в очереди и приедет на
	// следующем заходе. Ради этого outbox и заведён.
	queue := newFakeQueue(3)
	publisher := &fakePublisher{err: errors.New("шина недоступна")}

	runRelay(t, queue, publisher, newFakeWaiter())

	if pending := queue.pendingCount(); pending != 3 {
		t.Errorf("в очереди %d сообщений, ожидалось 3 — недоставленное потеряно", pending)
	}
	if attempts := queue.attempts(); attempts != 1 {
		t.Errorf("попыток учтено %d, ожидалась 1", attempts)
	}
}

func TestRelayWakesOnNotification(t *testing.T) {
	t.Parallel()

	// Очередь пополняется уже после того, как доставщик уснул: без побудки
	// он узнал бы о новом событии только по страховочному сроку.
	queue := newFakeQueue(0)
	publisher := &fakePublisher{}
	waiter := newFakeWaiter()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	relay := newRelay(t, queue, publisher, waiter)

	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()

	waiter.awaitWait(t)
	queue.append(2)
	waiter.notify()

	waitFor(t, func() bool { return publisher.count() == 2 })

	cancel()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("доставщик не остановился по отмене контекста")
	}
}

func TestRelayDrainsBeforeWaiting(t *testing.T) {
	t.Parallel()

	// Уведомления, случившиеся, пока доставщика не было, никто не повторит.
	// Поэтому первый заход обязан быть безусловным: очередь, наполненная до
	// запуска, уезжает без всякой побудки.
	queue := newFakeQueue(4)
	publisher := &fakePublisher{}
	waiter := newFakeWaiter()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	relay := newRelay(t, queue, publisher, waiter)
	go relay.Run(ctx)

	waitFor(t, func() bool { return publisher.count() == 4 })
}

func TestNewRelayRequiresDependencies(t *testing.T) {
	t.Parallel()

	queue, publisher, waiter := newFakeQueue(0), &fakePublisher{}, newFakeWaiter()

	cases := []struct {
		name      string
		queue     outbox.Queue
		publisher outbox.Publisher
		waiter    outbox.Waiter
	}{
		{"без очереди", nil, publisher, waiter},
		{"без публикатора", queue, nil, waiter},
		{"без побудки", queue, publisher, nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := outbox.NewRelay(c.queue, c.publisher, c.waiter); err == nil {
				t.Error("NewRelay(...) без зависимости вернул nil-ошибку")
			}
		})
	}
}

func TestNopPublisher(t *testing.T) {
	t.Parallel()

	if err := (outbox.NopPublisher{}).Publish(t.Context(), []outbox.Message{{ID: 1}}); err != nil {
		t.Errorf("Publish(...) вернул ошибку: %v", err)
	}
}

// runRelay гоняет доставщика до опустошения очереди и останавливает его.
func runRelay(t *testing.T, queue *fakeQueue, publisher *fakePublisher, waiter *fakeWaiter) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	relay := newRelay(t, queue, publisher, waiter)

	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()

	// Доставщик выгребает очередь до дна и только потом засыпает: дождавшись
	// ожидания, тест знает, что заход закончен.
	waiter.awaitWait(t)
	cancel()

	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("доставщик не остановился по отмене контекста")
	}
}

// newRelay собирает доставщика или валит тест.
func newRelay(t *testing.T, queue outbox.Queue, publisher outbox.Publisher, waiter outbox.Waiter) *outbox.Relay {
	t.Helper()

	relay, err := outbox.NewRelay(queue, publisher, waiter)
	if err != nil {
		t.Fatalf("NewRelay(...) вернул ошибку: %v", err)
	}
	return relay
}

// waitFor ждёт выполнения условия или валит тест.
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatal("условие не выполнилось за отведённое время")
}
