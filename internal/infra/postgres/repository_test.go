package postgres_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
	"github.com/deliseev/todoer/internal/infra"
	"github.com/deliseev/todoer/internal/infra/postgres"
	"github.com/deliseev/todoer/internal/infra/postgres/pgtest"
)

// Опорный момент. Хранилище о времени не думает — оно принимает то, что уже
// проставил домен, — поэтому здесь достаточно констант.
var testNow = time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)

// testOwner — владелец задач в тестах хранилища. Хранилище владельцев не
// различает: авторизация живёт в сценарии, а не на этой границе.
const testOwner = "user-42"

// TestOverdueTaskIsRestored: задача с просроченным сроком законно лежит в базе
// и обязана оттуда подняться.
//
// Порт этого не обещает — хранилищу в памяти нечего восстанавливать, оно
// держит доменные значения как есть, — а здесь срок собирается из колонки
// заново, и обычная фабрика NewDueDate отвергла бы его как срок в прошлом.
// Без ReconstituteDueDate просроченная задача оказалась бы заперта в базе.
func TestOverdueTaskIsRestored(t *testing.T) {
	t.Parallel()

	repo := newRepository(t)

	overdue := todotest.MustReconstituteDueDate(t, testNow.Add(-30*24*time.Hour))
	snapshot := todo.TaskSnapshot{
		ID:        todotest.MustTaskID(t),
		OwnerID:   todotest.MustOwnerID(t, testOwner),
		Title:     todotest.MustTitle(t, "Сдать отчёт"),
		Status:    todo.StatusPending,
		Priority:  todo.PriorityHigh,
		DueDate:   &overdue,
		CreatedAt: testNow.Add(-60 * 24 * time.Hour),
		UpdatedAt: testNow.Add(-60 * 24 * time.Hour),
		Version:   1,
	}

	task, err := todo.ReconstituteTask(snapshot)
	if err != nil {
		t.Fatalf("ReconstituteTask(...) вернул ошибку: %v", err)
	}
	if err := repo.Save(t.Context(), task, 0); err != nil {
		t.Fatalf("Save(...) вернул ошибку: %v", err)
	}

	got, _, err := repo.Get(t.Context(), task.ID())
	if err != nil {
		t.Fatalf("Get(...) вернул ошибку: %v", err)
	}

	gotDue, ok := got.DueDate()
	if !ok {
		t.Fatal("срок потерян при подъёме")
	}
	if !gotDue.Time().Equal(overdue.Time()) {
		t.Errorf("срок = %s, ожидался %s", gotDue.Time(), overdue.Time())
	}
	if !got.IsOverdue(testNow) {
		t.Error("поднятая задача не считается просроченной")
	}
}

// TestTimeResolution: договорённость о точности между часами и базой.
//
// timestamptz хранит микросекунды, поэтому наносекунды кругового пути не
// переживают. Момент, выданный часами, переживает его тождественно — ради
// этого SystemClock и усекает время. Проверка держит обе половины
// договорённости: сними усечение в часах — и первый подтест покажет, что
// круговой путь перестал быть тождественным.
func TestTimeResolution(t *testing.T) {
	t.Parallel()

	t.Run("момент часов переживает круговой путь тождественно", func(t *testing.T) {
		t.Parallel()

		now := infra.SystemClock{}.Now()

		got := saveAndGet(t, todotest.NewTask(t, testOwner, now))

		if !got.CreatedAt().Equal(now) {
			t.Errorf("createdAt = %s, ожидалось %s",
				got.CreatedAt().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		}
	})

	t.Run("наносекунды теряются", func(t *testing.T) {
		t.Parallel()

		// Момент с наносекундным остатком — то, что отдали бы часы без
		// усечения. База хранит микросекунды, и разница здесь не «почти
		// равно», а другое значение.
		now := time.Date(2026, time.August, 13, 12, 0, 0, 123456789, time.UTC)

		got := saveAndGet(t, todotest.NewTask(t, testOwner, now))

		if got.CreatedAt().Equal(now) {
			t.Fatalf("createdAt = %s: база сохранила наносекунды, чего timestamptz не умеет",
				got.CreatedAt().Format(time.RFC3339Nano))
		}
		if diff := got.CreatedAt().Sub(now).Abs(); diff >= time.Microsecond {
			t.Errorf("createdAt разошёлся на %s, ожидалось меньше микросекунды", diff)
		}
	})
}

// TestRowWithUnknownStatusRejected: строку в базе мог написать кто угодно —
// прежняя версия кода, миграция, рука в psql, — и доверять ей на слово
// хранилище не вправе.
//
// Порт этого не обещает: в память попадают только снимки живых агрегатов,
// и там ветка отказа недостижима.
//
// Доменный сентинель при этом наружу не выпускается: по ErrUnknownStatus
// транспорт отвечает 400, а запрос клиента был безупречен — испорчена строка
// в базе, и починить её он не может. Такое врёт клиенту и прячет аварию от
// того, кто следит за пятисотками.
func TestRowWithUnknownStatusRejected(t *testing.T) {
	t.Parallel()

	dsn := pgtest.NewDSN(t)

	pool, err := postgres.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("подключение к базе теста: %v", err)
	}
	t.Cleanup(pool.Close)

	repo := postgres.NewTaskRepository(pool)
	task := todotest.NewTask(t, testOwner, testNow)

	if err := repo.Save(t.Context(), task, 0); err != nil {
		t.Fatalf("Save(...) вернул ошибку: %v", err)
	}

	// Порча вносится в обход хранилища: изнутри такое состояние недостижимо,
	// а снаружи — вполне.
	if _, err := pool.Exec(t.Context(),
		"UPDATE tasks SET status = $1 WHERE id = $2", "неизвестный", task.ID().String(),
	); err != nil {
		t.Fatalf("порча строки: %v", err)
	}

	got, _, err := repo.Get(t.Context(), task.ID())

	if err == nil {
		t.Fatal("Get(...) поднял задачу с неизвестным статусом")
	}
	if errors.Is(err, todo.ErrUnknownStatus) {
		t.Errorf("Get(...) вернул доменный сентинель (%v): транспорт ответит по нему 400 вместо 500", err)
	}
	// Причина остаётся в тексте: её читают в логе, когда уже всё сломалось.
	if !strings.Contains(err.Error(), todo.ErrUnknownStatus.Error()) {
		t.Errorf("ошибка = %q, ожидалось упоминание причины %q", err, todo.ErrUnknownStatus)
	}
	if got != nil {
		t.Error("Get(...) вернул задачу вместе с ошибкой")
	}
}

// TestSchemaVersion: команда миграций и приложение обязаны сходиться в том,
// какая схема считается текущей.
func TestSchemaVersion(t *testing.T) {
	t.Parallel()

	t.Run("отставшая база — отказ запуска", func(t *testing.T) {
		t.Parallel()

		// База пустая: схемы в ней нет вовсе, а это и есть предельный случай
		// отставания.
		dsn := pgtest.NewEmptyDSN(t)

		err := postgres.EnsureSchema(t.Context(), dsn)

		if !errors.Is(err, postgres.ErrSchemaOutdated) {
			t.Errorf("EnsureSchema(...) вернул ошибку %v, ожидалась ErrSchemaOutdated", err)
		}
	})

	t.Run("накатанная схема принимается", func(t *testing.T) {
		t.Parallel()

		dsn := pgtest.NewDSN(t)

		if err := postgres.EnsureSchema(t.Context(), dsn); err != nil {
			t.Errorf("EnsureSchema(...) на накатанной схеме вернул ошибку: %v", err)
		}
	})
}

// TestMigrate проверяет сам путь наката — тот, которым схему двигает выкатка.
func TestMigrate(t *testing.T) {
	t.Parallel()

	t.Run("накат с нуля доводит схему до ожидаемой версии", func(t *testing.T) {
		t.Parallel()

		dsn := pgtest.NewEmptyDSN(t)

		if err := postgres.Migrate(t.Context(), dsn); err != nil {
			t.Fatalf("Migrate(...) вернул ошибку: %v", err)
		}

		expected, err := postgres.ExpectedSchemaVersion()
		if err != nil {
			t.Fatalf("ExpectedSchemaVersion() вернула ошибку: %v", err)
		}

		got, err := postgres.SchemaVersion(t.Context(), dsn)
		if err != nil {
			t.Fatalf("SchemaVersion(...) вернула ошибку: %v", err)
		}
		if got != expected {
			t.Errorf("версия схемы = %d, ожидалась %d", got, expected)
		}
	})

	t.Run("повторный накат ничего не ломает", func(t *testing.T) {
		t.Parallel()

		// Выкатка запускает миграции каждый раз, в том числе когда двигать
		// нечего: накат обязан быть идемпотентным, а не падать на второй раз.
		dsn := pgtest.NewEmptyDSN(t)

		for attempt := range 3 {
			if err := postgres.Migrate(t.Context(), dsn); err != nil {
				t.Fatalf("Migrate(...) на попытке %d вернул ошибку: %v", attempt+1, err)
			}
		}
	})

	t.Run("откат снимает последнюю миграцию", func(t *testing.T) {
		t.Parallel()

		// Ожидание считается от вшитых миграций, а не задаётся числом: команда
		// обещает шаг назад, и это обещание не должно переписываться при
		// каждой новой миграции.
		expected, err := postgres.ExpectedSchemaVersion()
		if err != nil {
			t.Fatalf("ExpectedSchemaVersion() вернула ошибку: %v", err)
		}

		dsn := pgtest.NewDSN(t)

		if err := postgres.Rollback(t.Context(), dsn); err != nil {
			t.Fatalf("Rollback(...) вернул ошибку: %v", err)
		}

		version, err := postgres.SchemaVersion(t.Context(), dsn)
		if err != nil {
			t.Fatalf("SchemaVersion(...) вернула ошибку: %v", err)
		}
		if version != expected-1 {
			t.Errorf("версия схемы после отката = %d, ожидалась %d", version, expected-1)
		}
	})
}

// TestStatusListsMigrations: состояние миграций читается тем же кодом, что
// печатает команда.
func TestStatusListsMigrations(t *testing.T) {
	t.Parallel()

	dsn := pgtest.NewDSN(t)

	statuses, err := postgres.Status(t.Context(), dsn)
	if err != nil {
		t.Fatalf("Status(...) вернул ошибку: %v", err)
	}
	if len(statuses) == 0 {
		t.Fatal("Status(...) не вернул ни одной миграции")
	}

	for _, status := range statuses {
		if !status.Applied {
			t.Errorf("миграция %d (%s) не накатана в базе, созданной из шаблона",
				status.Version, status.Source)
		}
	}
}

// saveAndGet сохраняет задачу и поднимает её обратно.
func saveAndGet(t *testing.T, task *todo.Task) *todo.Task {
	t.Helper()

	repo := newRepository(t)

	if err := repo.Save(t.Context(), task, 0); err != nil {
		t.Fatalf("Save(...) вернул ошибку: %v", err)
	}

	got, _, err := repo.Get(t.Context(), task.ID())
	if err != nil {
		t.Fatalf("Get(...) вернул ошибку: %v", err)
	}

	return got
}
