package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/infra"
	"github.com/deliseev/todoer/internal/infra/postgres"
	"github.com/deliseev/todoer/internal/infra/postgres/pgtest"
)

// Опорный момент. Хранилище о времени не думает — оно принимает то, что уже
// проставил домен, — поэтому здесь достаточно констант.
var testNow = time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)

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

	overdue := mustReconstituteDueDate(t, testNow.Add(-30*24*time.Hour))
	snapshot := todo.TaskSnapshot{
		ID:        mustTaskID(t),
		OwnerID:   mustOwnerID(t, "user-42"),
		Title:     mustTitle(t, "Сдать отчёт"),
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

		got := saveAndGet(t, newTaskAt(t, now))

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

		got := saveAndGet(t, newTaskAt(t, now))

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
func TestRowWithUnknownStatusRejected(t *testing.T) {
	t.Parallel()

	dsn := pgtest.NewDSN(t)

	pool, err := postgres.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("подключение к базе теста: %v", err)
	}
	t.Cleanup(pool.Close)

	repo := postgres.NewTaskRepository(pool)
	task := newTaskAt(t, testNow)

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

	if !errors.Is(err, todo.ErrUnknownStatus) {
		t.Errorf("Get(...) вернул ошибку %v, ожидалась ErrUnknownStatus", err)
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

	t.Run("откат снимает схему", func(t *testing.T) {
		t.Parallel()

		dsn := pgtest.NewDSN(t)

		if err := postgres.Rollback(t.Context(), dsn); err != nil {
			t.Fatalf("Rollback(...) вернул ошибку: %v", err)
		}

		version, err := postgres.SchemaVersion(t.Context(), dsn)
		if err != nil {
			t.Fatalf("SchemaVersion(...) вернула ошибку: %v", err)
		}
		if version != 0 {
			t.Errorf("версия схемы после отката = %d, ожидался ноль", version)
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

// newTaskAt создаёт задачу, созданную в момент now.
func newTaskAt(t *testing.T, now time.Time) *todo.Task {
	t.Helper()

	dueDate, err := todo.NewDueDate(now.Add(24*time.Hour), now)
	if err != nil {
		t.Fatalf("NewDueDate(...) вернул ошибку: %v", err)
	}

	task, err := todo.NewTask(
		mustTaskID(t),
		mustOwnerID(t, "user-42"),
		mustTitle(t, "Купить молоко"),
		todo.Description{},
		todo.PriorityNormal,
		&dueDate,
		now,
	)
	if err != nil {
		t.Fatalf("NewTask(...) вернул ошибку: %v", err)
	}

	return task
}

func mustTaskID(t *testing.T) todo.TaskID {
	t.Helper()

	id, err := todo.NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() вернул ошибку: %v", err)
	}
	return id
}

func mustOwnerID(t *testing.T, s string) todo.OwnerID {
	t.Helper()

	owner, err := todo.ParseOwnerID(s)
	if err != nil {
		t.Fatalf("ParseOwnerID(%q) вернул ошибку: %v", s, err)
	}
	return owner
}

func mustTitle(t *testing.T, s string) todo.Title {
	t.Helper()

	title, err := todo.NewTitle(s)
	if err != nil {
		t.Fatalf("NewTitle(%q) вернул ошибку: %v", s, err)
	}
	return title
}

func mustReconstituteDueDate(t *testing.T, at time.Time) todo.DueDate {
	t.Helper()

	dueDate, err := todo.ReconstituteDueDate(at)
	if err != nil {
		t.Fatalf("ReconstituteDueDate(%s) вернул ошибку: %v", at, err)
	}
	return dueDate
}
