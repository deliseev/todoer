// Команда todoer-migrate двигает схему базы.
//
// Отдельная команда, а не шаг при старте приложения: схему двигает выкатка.
// Молчаливый накат при старте означал бы, что два поднимающихся экземпляра
// мигрируют базу одновременно, а откатить неудачную миграцию нельзя, не
// остановив их обоих. Приложение вместо этого сверяет версию схемы и
// отказывается работать на отставшей.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/deliseev/todoer/internal/infra/postgres"
)

// dsnEnv — переменная окружения со строкой подключения. Та же, что у
// приложения: схема и код обязаны смотреть в одну базу.
const dsnEnv = "TODOER_DATABASE_URL"

// errUsage — команда вызвана неправильно.
var errUsage = errors.New("usage: todoer-migrate up|down|status")

func main() {
	// Миграция может идти долго, и оборвать её по Ctrl+C должно быть можно:
	// отмена доедет до базы контекстом.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Stdout, os.Args[1:], databaseURL()); err != nil {
		fmt.Fprintln(os.Stderr, "todoer-migrate:", err)
		os.Exit(1)
	}
}

// databaseURL читает строку подключения из окружения.
// Пустая переменная равна отсутствующей: пробелы — не адрес.
func databaseURL() string {
	return strings.TrimSpace(os.Getenv(dsnEnv))
}

// run выполняет команду. Отделён от main, чтобы его мог водить тест: аргументы
// и строка подключения приезжают параметрами, вывод — в out.
func run(ctx context.Context, out io.Writer, args []string, dsn string) error {
	if len(args) != 1 {
		return errUsage
	}

	// Проверяется до разбора команды: без базы не выполнима ни одна из них,
	// и сказать об этом лучше раньше, чем после разбора аргументов.
	if dsn == "" {
		return fmt.Errorf("%s is not set", dsnEnv)
	}

	switch args[0] {
	case "up":
		if err := postgres.Migrate(ctx, dsn); err != nil {
			return err
		}
		return printStatus(ctx, out, dsn)

	case "down":
		if err := postgres.Rollback(ctx, dsn); err != nil {
			return err
		}
		return printStatus(ctx, out, dsn)

	case "status":
		return printStatus(ctx, out, dsn)

	default:
		return fmt.Errorf("unknown command %q: %w", args[0], errUsage)
	}
}

// printStatus печатает состояние каждой миграции.
//
// Печатается и после наката: «сделано» без перечисления того, что именно
// сделано, заставляет лезть в базу руками.
func printStatus(ctx context.Context, out io.Writer, dsn string) error {
	statuses, err := postgres.Status(ctx, dsn)
	if err != nil {
		return err
	}

	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	fmt.Fprintln(table, "ВЕРСИЯ\tСОСТОЯНИЕ\tМИГРАЦИЯ")
	for _, status := range statuses {
		state := "не накатана"
		if status.Applied {
			state = "накатана"
		}
		fmt.Fprintf(table, "%d\t%s\t%s\n", status.Version, state, status.Source)
	}

	return table.Flush()
}
