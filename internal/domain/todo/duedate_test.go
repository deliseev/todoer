package todo_test

import (
	"errors"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
)

func TestNewDueDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		at      time.Time
		wantErr error
	}{
		{
			name: "срок в будущем принимается",
			at:   testNow.Add(24 * time.Hour),
		},
		{
			name: "срок на наносекунду позже now принимается",
			at:   testNow.Add(time.Nanosecond),
		},
		{
			name:    "срок ровно в момент now отвергается",
			at:      testNow,
			wantErr: todo.ErrDueDateInPast,
		},
		{
			name:    "срок на наносекунду раньше now отвергается",
			at:      testNow.Add(-time.Nanosecond),
			wantErr: todo.ErrDueDateInPast,
		},
		{
			name:    "срок в прошлом отвергается",
			at:      testNow.Add(-24 * time.Hour),
			wantErr: todo.ErrDueDateInPast,
		},
		{
			name:    "нулевое время отвергается",
			at:      time.Time{},
			wantErr: todo.ErrDueDateInPast,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := todo.NewDueDate(tt.at, testNow)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewDueDate(...) вернул ошибку %v, ожидалась %v", err, tt.wantErr)
				}
				if !got.IsZero() {
					t.Error("NewDueDate(...) при ошибке вернул непустой срок")
				}
				return
			}

			if err != nil {
				t.Fatalf("NewDueDate(...) вернул неожиданную ошибку: %v", err)
			}
			if !got.Time().Equal(tt.at) {
				t.Errorf("DueDate.Time() = %s, ожидалось %s", got.Time(), tt.at)
			}
			if got.IsZero() {
				t.Error("DueDate.IsZero() = true, ожидалось false для созданного срока")
			}
		})
	}
}

func TestDueDateNormalizedToUTC(t *testing.T) {
	t.Parallel()

	// Одна и та же точка времени, записанная в другой зоне, обязана дать
	// равное значение: иначе два одинаковых срока перестанут быть равны.
	moscow := time.FixedZone("MSK", 3*60*60)
	at := testNow.Add(24 * time.Hour).In(moscow)

	due, err := todo.NewDueDate(at, testNow)
	if err != nil {
		t.Fatalf("NewDueDate(...) вернул ошибку: %v", err)
	}

	if loc := due.Time().Location(); loc != time.UTC {
		t.Errorf("DueDate.Time().Location() = %s, ожидалось UTC", loc)
	}
	if !due.Time().Equal(at) {
		t.Errorf("DueDate.Time() = %s, ожидалась та же точка времени, что и %s", due.Time(), at)
	}

	sameInUTC, err := todo.NewDueDate(at.UTC(), testNow)
	if err != nil {
		t.Fatalf("NewDueDate(...) вернул ошибку: %v", err)
	}
	if due != sameInUTC {
		t.Error("сроки, заданные в разных зонах для одной точки времени, должны быть равны")
	}
}

func TestNewDueDateComparesInstants(t *testing.T) {
	t.Parallel()

	msk := time.FixedZone("MSK", 3*60*60)
	at := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	// time.Now несёт монотонные часы — так его вернёт SystemClock в бою.
	monotonic := time.Now()

	// Во всех случаях срок совпадает с now с точностью до наносекунды,
	// то есть «в будущем» не наступает и должен быть отвергнут.
	tests := []struct {
		name string
		at   time.Time
		now  time.Time
	}{
		{name: "оба момента в UTC", at: at, now: at},
		{name: "now записан в другой зоне", at: at, now: at.In(msk)},
		{name: "срок записан в другой зоне", at: at.In(msk), now: at},
		{name: "now с монотонными часами", at: monotonic, now: monotonic},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := todo.NewDueDate(tt.at, tt.now); !errors.Is(err, todo.ErrDueDateInPast) {
				t.Errorf("NewDueDate(%s, %s) вернул ошибку %v, ожидалась ErrDueDateInPast",
					tt.at, tt.now, err)
			}
		})
	}
}

func TestDueDateIsBefore(t *testing.T) {
	t.Parallel()

	due := mustDueDate(t, testNow.Add(24*time.Hour))

	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{name: "момент до срока", at: testNow, want: false},
		{name: "момент ровно в срок", at: testNow.Add(24 * time.Hour), want: false},
		{name: "момент после срока", at: testNow.Add(25 * time.Hour), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := due.IsBefore(tt.at); got != tt.want {
				t.Errorf("DueDate.IsBefore(%s) = %v, ожидалось %v", tt.at, got, tt.want)
			}
		})
	}
}

func TestDueDateZeroValue(t *testing.T) {
	t.Parallel()

	var due todo.DueDate

	if !due.IsZero() {
		t.Error("DueDate{}.IsZero() = false, ожидалось true")
	}
	if !due.Time().IsZero() {
		t.Errorf("DueDate{}.Time() = %s, ожидалось нулевое время", due.Time())
	}
}
