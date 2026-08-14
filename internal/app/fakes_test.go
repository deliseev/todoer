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
func (r *fakeRepository) Get(_ context.Context, id todo.TaskID) (*todo.Task, error) {
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
func (r *fakeRepository) Save(_ context.Context, task *todo.Task) error {
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

	snapshot := task.Snapshot()
	if stored, ok := r.tasks[snapshot.ID.String()]; ok && stored.Version >= snapshot.Version {
		return fmt.Errorf("%w: сохранено %d, записывается %d",
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
}

// Publish запоминает партию событий.
func (p *recordingPublisher) Publish(_ context.Context, events []todo.DomainEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++
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

	names := make([]string, len(p.events))
	for i, e := range p.events {
		names[i] = e.EventName()
	}
	return names
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
