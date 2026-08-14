package infra

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

// errNilTask — вместо задачи передали nil.
//
// Хранилище стоит на границе, и мусор с той стороны обязано пережить:
// забытая наверху проверка ошибки не повод ронять процесс целиком.
var errNilTask = errors.New("infra: task is nil")

// InMemoryTaskRepository — реализация app.Repository поверх памяти.
//
// Хранит снимки, а не указатели на агрегат: иначе вызывающий держал бы тот же
// объект, что и хранилище, и менял бы задачу в обход Save, версии и событий.
//
// Оптимистичная блокировка строгая: обновление принимается только следующей
// версией. Задача, ушедшая вперёд через голову хранилища, поднята не отсюда,
// и между сохранённым и записываемым состоянием потерялись мутации.
// Первая запись версией при этом не ограничена — агрегат не обязан
// сохраняться после каждой мутации.
type InMemoryTaskRepository struct {
	// RWMutex, а не Mutex: чтений у хранилища заметно больше, чем записей,
	// и держать их в одной очереди незачем.
	mu        sync.RWMutex
	snapshots map[todo.TaskID]todo.TaskSnapshot
}

// NewInMemoryTaskRepository создаёт пустое хранилище.
func NewInMemoryTaskRepository() *InMemoryTaskRepository {
	return &InMemoryTaskRepository{
		snapshots: make(map[todo.TaskID]todo.TaskSnapshot),
	}
}

// Get поднимает задачу из памяти.
func (r *InMemoryTaskRepository) Get(ctx context.Context, taskID todo.TaskID) (*todo.Task, error) {
	// Контекст проверяется до блокировки: отменённому запросу незачем ждать
	// очереди, чтобы узнать, что он никому не нужен.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	snapshot, ok := r.snapshot(taskID)
	if !ok {
		return nil, fmt.Errorf("%w: задача %s", app.ErrTaskNotFound, taskID)
	}

	// Восстановление идёт уже без блокировки: снимок скопирован, а работа
	// домена хранилища не касается.
	task, err := todo.ReconstituteTask(snapshot)
	if err != nil {
		return nil, fmt.Errorf("infra: восстановить задачу %s: %w", taskID, err)
	}

	return task, nil
}

// Save сохраняет задачу, соблюдая оптимистичную блокировку по версии.
func (r *InMemoryTaskRepository) Save(ctx context.Context, task *todo.Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if task == nil {
		return errNilTask
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	version := task.Version()
	if stored, ok := r.snapshots[task.ID()]; ok && stored.Version+1 != version {
		return fmt.Errorf("%w: сохранена версия %d, записывается %d",
			app.ErrVersionConflict, stored.Version, version)
	}

	r.snapshots[task.ID()] = task.Snapshot()

	return nil
}

// snapshot отдаёт копию сохранённого снимка под блокировкой чтения.
func (r *InMemoryTaskRepository) snapshot(taskID todo.TaskID) (todo.TaskSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot, ok := r.snapshots[taskID]
	return snapshot, ok
}
