package httpapi_test

import (
	"context"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

// fakeService — двойник слоя сценариев.
//
// Хендлеры проверяются на нём, а не на настоящем TaskService с хранилищем
// в памяти: транспорт отвечает за разбор запроса и перевод ошибки в код,
// и подставить ему нужную ошибку — единственный способ проверить перевод.
// На живом сервисе половину из них (конфликт версий, отказ доставки, отмену
// контекста) пришлось бы подстраивать через хранилище, и тест проверял бы
// не транспорт, а собственную изобретательность.
//
// Команды запоминаются целиком: разбор запроса в команду — тоже работа
// транспорта, и проверять её надо по тому, что уехало в сценарий.
type fakeService struct {
	// Что вернуть.
	createID  todo.TaskID
	createErr error
	view      app.TaskView
	getErr    error
	updateErr error
	startErr  error
	doneErr   error
	cancelErr error

	// Что приехало.
	createCmd app.CreateTaskCommand
	getQuery  app.GetTaskQuery
	updateCmd app.UpdateTaskCommand
	startCmd  app.StartTaskCommand
	doneCmd   app.CompleteTaskCommand
	cancelCmd app.CancelTaskCommand

	// Сколько раз звали — по одному счётчику на сценарий: негодный запрос
	// не должен доходить до слоя сценариев вовсе.
	calls map[string]int
}

func newFakeService() *fakeService {
	return &fakeService{calls: make(map[string]int)}
}

func (s *fakeService) record(name string) {
	if s.calls == nil {
		s.calls = make(map[string]int)
	}
	s.calls[name]++
}

// callCount возвращает число обращений к сценарию.
func (s *fakeService) callCount(name string) int {
	return s.calls[name]
}

// totalCalls возвращает число обращений ко всем сценариям сразу — так тест
// проверяет, что негодный запрос не дошёл до app вообще.
func (s *fakeService) totalCalls() int {
	total := 0
	for _, n := range s.calls {
		total += n
	}
	return total
}

func (s *fakeService) CreateTask(_ context.Context, cmd app.CreateTaskCommand) (todo.TaskID, error) {
	s.record("CreateTask")
	s.createCmd = cmd
	return s.createID, s.createErr
}

func (s *fakeService) GetTask(_ context.Context, query app.GetTaskQuery) (app.TaskView, error) {
	s.record("GetTask")
	s.getQuery = query
	if s.getErr != nil {
		return app.TaskView{}, s.getErr
	}
	return s.view, nil
}

func (s *fakeService) UpdateTask(_ context.Context, cmd app.UpdateTaskCommand) error {
	s.record("UpdateTask")
	s.updateCmd = cmd
	return s.updateErr
}

func (s *fakeService) StartTask(_ context.Context, cmd app.StartTaskCommand) error {
	s.record("StartTask")
	s.startCmd = cmd
	return s.startErr
}

func (s *fakeService) CompleteTask(_ context.Context, cmd app.CompleteTaskCommand) error {
	s.record("CompleteTask")
	s.doneCmd = cmd
	return s.doneErr
}

func (s *fakeService) CancelTask(_ context.Context, cmd app.CancelTaskCommand) error {
	s.record("CancelTask")
	s.cancelCmd = cmd
	return s.cancelErr
}
