// Команда todoer — композиционный корень приложения.
//
// Здесь и только здесь слои встречаются: инфраструктура подставляется в порты
// сценариев, а те работают с доменом. Ни один пакет ниже про эту сборку
// не знает и знать не должен.
//
// Транспорта пока нет, поэтому команда прогоняет короткий сценарий и печатает
// результат: это дымовая проверка того, что собранное приложение живо.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/infra"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "todoer:", err)
		os.Exit(1)
	}
}

// newTaskService собирает сервис на боевых реализациях портов.
//
// Публикатор пока заглушечный: доставка событий появится вместе с
// транспортом. Собирать сервис с nil вместо него нельзя — это тихая потеря
// событий, и конструктор такое отвергает.
func newTaskService() (*app.TaskService, error) {
	return app.NewTaskService(
		infra.NewInMemoryTaskRepository(),
		infra.NopPublisher{},
		infra.SystemClock{},
	)
}

// run прогоняет жизненный цикл одной задачи через собранное приложение.
func run(ctx context.Context, out io.Writer) error {
	service, err := newTaskService()
	if err != nil {
		return fmt.Errorf("build task service: %w", err)
	}

	const owner = "user-42"
	dueDate := time.Now().Add(24 * time.Hour)

	taskID, err := service.CreateTask(ctx, app.CreateTaskCommand{
		OwnerID:     owner,
		Title:       "Купить молоко",
		Description: "Два литра, в магазине у дома",
		Priority:    "high",
		DueDate:     &dueDate,
	})
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	fmt.Fprintf(out, "задача создана: %s\n", taskID)

	if err := service.StartTask(ctx, app.StartTaskCommand{
		TaskID:  taskID.String(),
		OwnerID: owner,
	}); err != nil {
		return fmt.Errorf("start task: %w", err)
	}
	fmt.Fprintln(out, "задача в работе")

	if err := service.CompleteTask(ctx, app.CompleteTaskCommand{
		TaskID:  taskID.String(),
		OwnerID: owner,
	}); err != nil {
		return fmt.Errorf("complete task: %w", err)
	}
	fmt.Fprintln(out, "задача выполнена")

	return nil
}
