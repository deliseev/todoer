package infra_test

import (
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/infra"
)

// Системные часы обязаны удовлетворять порту app.Clock.
var _ app.Clock = infra.SystemClock{}

func TestSystemClockNow(t *testing.T) {
	t.Parallel()

	// Границы усечены так же, как усекает сама реализация: иначе нижняя из них
	// оказалась бы на доли микросекунды позже полученного момента.
	before := time.Now().Truncate(time.Microsecond)
	got := infra.SystemClock{}.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("SystemClock.Now() = %s, ожидалось значение между %s и %s", got, before, after)
	}
	if loc := got.Location(); loc != time.UTC {
		t.Errorf("SystemClock.Now().Location() = %s, ожидалось UTC", loc)
	}
}

// TestSystemClockTruncatedToMicrosecond: часы не отдают наносекунд, потому что
// хранилище их не сохранит.
//
// timestamptz в Postgres хранит микросекунды, а time.Now() даёт наносекунды —
// и круговой путь через базу перестал бы быть тождественным: записали один
// момент, прочитали другой. Часы и хранилище — обе инфраструктура, и
// договориться о разрешающей способности они обязаны между собой, а не за
// счёт домена, который сравнивает моменты через Equal и вправе ждать
// равенства.
//
// Проверка ловит снятое усечение не везде: на macOS time.Now() и без того
// отдаёт микросекунды, и там тест зелёный сам по себе. Кусается он на Linux,
// то есть в CI. Детерминированная проверка того же правила — круговой путь
// через базу в тестах хранилища, где усечение делает уже Postgres.
func TestSystemClockTruncatedToMicrosecond(t *testing.T) {
	t.Parallel()

	// Одного вызова мало: наносекундный остаток мог оказаться нулевым сам по
	// себе, и тест был бы зелёным по неверной причине.
	for range 100 {
		got := infra.SystemClock{}.Now()

		if rest := got.Nanosecond() % 1000; rest != 0 {
			t.Fatalf("SystemClock.Now() = %s, остаток %d нс: ожидалось усечение до микросекунды",
				got.Format(time.RFC3339Nano), rest)
		}
	}
}
