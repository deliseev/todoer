package app_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

// fakeRepository — хранилище в памяти для тестов сценариев.
//
// Хранит именно снимки, а не указатели на агрегат: иначе сценарий, забывший
// вызвать Save, всё равно «сохранял» бы изменения через общий объект,
// и тест этого не заметил бы.
type fakeRepository struct {
	mu    sync.Mutex
	tasks map[string]todo.TaskSnapshot

	// getErr и saveErr подменяют результат порта, чтобы проверить поведение
	// сценария при отказе хранилища.
	getErr  error
	saveErr error

	// beforeSave вызывается перед записью и без удерживаемого мьютекса:
	// так тест изображает чужую запись, вклинившуюся между Get и Save.
	beforeSave func()

	saves int
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{tasks: make(map[string]todo.TaskSnapshot)}
}

// Get поднимает задачу из памяти.
func (r *fakeRepository) Get(ctx context.Context, id todo.TaskID) (*todo.Task, error) {
	// Настоящее хранилище отменённый запрос не обслуживает, и фейк, который
	// обслуживал бы, разрешал бы сценарию работать после отмены незаметно.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.getErr != nil {
		return nil, r.getErr
	}

	snapshot, ok := r.tasks[id.String()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", app.ErrTaskNotFound, id)
	}
	return todo.ReconstituteTask(snapshot)
}

// Save записывает задачу, соблюдая оптимистичную блокировку по версии.
func (r *fakeRepository) Save(ctx context.Context, task *todo.Task) error {
	// Контекст проверяется до хука: хук изображает отмену, случившуюся уже
	// после того, как хранилище приняло запись.
	if err := ctx.Err(); err != nil {
		return err
	}

	if r.beforeSave != nil {
		hook := r.beforeSave
		r.beforeSave = nil
		hook()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.saveErr != nil {
		return r.saveErr
	}

	// То же правило, что у настоящего хранилища: обновление принимается
	// только следующей версией.
	snapshot := task.Snapshot()
	if stored, ok := r.tasks[snapshot.ID.String()]; ok && stored.Version+1 != snapshot.Version {
		return fmt.Errorf("%w: сохранена версия %d, записывается %d",
			app.ErrVersionConflict, stored.Version, snapshot.Version)
	}

	r.tasks[snapshot.ID.String()] = snapshot
	r.saves++

	return nil
}

// stored возвращает сохранённый снимок задачи.
func (r *fakeRepository) stored(id todo.TaskID) (todo.TaskSnapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	snapshot, ok := r.tasks[id.String()]
	return snapshot, ok
}

// put кладёт снимок в обход проверки версий — так тест изображает запись,
// сделанную кем-то другим между Get и Save сценария.
func (r *fakeRepository) put(snapshot todo.TaskSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tasks[snapshot.ID.String()] = snapshot
}

// saveCount возвращает число состоявшихся записей.
func (r *fakeRepository) saveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.saves
}

// recordingPublisher запоминает всё, что сценарии отдали на публикацию.
type recordingPublisher struct {
	mu     sync.Mutex
	events []todo.DomainEvent
	calls  int
	err    error
	// sawEmpty отмечает публикацию пустой партии: успешная мутация всегда
	// порождает событие, поэтому дёргать публикатор впустую сценарию незачем.
	sawEmpty bool
	// ctxErr — состояние контекста на момент вызова. Сам контекст в поле
	// не кладём: он живёт ровно столько, сколько вызов.
	ctxErr error
}

// Publish запоминает партию событий.
func (p *recordingPublisher) Publish(ctx context.Context, events []todo.DomainEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++
	p.ctxErr = ctx.Err()
	if len(events) == 0 {
		p.sawEmpty = true
	}
	p.events = append(p.events, events...)

	return p.err
}

// published возвращает имена опубликованных событий в порядке публикации.
func (p *recordingPublisher) published() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return eventNames(p.events)
}

// stubClock — управляемые часы. Тесты двигают их вручную, поэтому «сейчас»
// в сценариях остаётся таким же предсказуемым, как в тестах домена.
type stubClock struct {
	mu sync.Mutex
	at time.Time
}

// Now возвращает выставленный момент времени.
func (c *stubClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.at
}

// set переводит часы на заданный момент.
func (c *stubClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.at = at
}
