package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewTaskService(t *testing.T) {
	t.Parallel()

	t.Run("боевые реализации подходят портам", func(t *testing.T) {
		t.Parallel()

		service, err := newTaskService()
		if err != nil {
			t.Fatalf("newTaskService() вернул ошибку: %v", err)
		}
		if service == nil {
			t.Fatal("newTaskService() вернул nil без ошибки")
		}
	})
}

func TestListenAddr(t *testing.T) {
	// Без t.Parallel: подтесты правят окружение процесса.

	t.Run("по умолчанию", func(t *testing.T) {
		t.Setenv(addrEnv, "")

		if got := listenAddr(); got != defaultAddr {
			t.Errorf("listenAddr() = %q, ожидался %q", got, defaultAddr)
		}
	})

	t.Run("из окружения", func(t *testing.T) {
		t.Setenv(addrEnv, " 127.0.0.1:9999 ")

		// Пробелы по краям — след копирования из конфига, а не часть адреса.
		if got := listenAddr(); got != "127.0.0.1:9999" {
			t.Errorf("listenAddr() = %q, ожидался %q", got, "127.0.0.1:9999")
		}
	})
}

func TestRestoreSignalsOnCancel(t *testing.T) {
	t.Parallel()

	// Первый сигнал отменяет контекст и запускает остановку, которая длится
	// до десяти секунд. Всё это время обработчик сигналов обязан быть снят:
	// иначе второй Ctrl+C нетерпеливого оператора будет проглочен — контекст
	// уже отменён, и делать этому сигналу нечего.
	restored := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())

	restoreSignalsOnCancel(ctx, func() { close(restored) })

	select {
	case <-restored:
		t.Fatal("поведение сигналов восстановлено до отмены: первый же сигнал убьёт процесс на месте")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case <-restored:
	case <-time.After(2 * time.Second):
		t.Fatal("после отмены поведение сигналов не восстановлено: второй сигнал будет проглочен")
	}
}

func TestNewServer(t *testing.T) {
	t.Parallel()

	// Нулевой таймаут у http.Server значит «ждать вечно», поэтому проверяется
	// именно ненулевость. Одного ReadHeaderTimeout мало: клиент, приславший
	// корректные заголовки и сочащий тело по байту, застревает уже в чтении
	// тела, а спящий в keep-alive — вообще ни в одной из фаз запроса.
	server := newServer(http.NewServeMux())

	timeouts := []struct {
		name string
		got  time.Duration
	}{
		{"ReadHeaderTimeout", server.ReadHeaderTimeout},
		{"ReadTimeout", server.ReadTimeout},
		{"WriteTimeout", server.WriteTimeout},
		{"IdleTimeout", server.IdleTimeout},
	}

	for _, timeout := range timeouts {
		if timeout.got <= 0 {
			t.Errorf("%s = %s, ожидался ненулевой таймаут", timeout.name, timeout.got)
		}
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("собранное приложение обслуживает запросы", func(t *testing.T) {
		t.Parallel()

		// Проверяется вся сборка целиком: транспорт поверх сценариев поверх
		// хранилища в памяти. Задача создаётся и тут же читается обратно —
		// значит слои соединены, а не просто скомпилировались.
		out := &syncBuffer{}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)

		go func() { done <- run(ctx, out, "127.0.0.1:0") }()

		addr := waitAddr(t, out)

		created := do(t, request(t, http.MethodPost, "http://"+addr+"/tasks",
			`{"title":"Купить молоко","priority":"high"}`))
		if created.StatusCode != http.StatusCreated {
			t.Fatalf("код ответа на создание = %d, ожидался %d", created.StatusCode, http.StatusCreated)
		}

		location := created.Header.Get("Location")
		if location == "" {
			t.Fatal("в ответе на создание нет Location")
		}

		read := do(t, request(t, http.MethodGet, "http://"+addr+location, ""))
		if read.StatusCode != http.StatusOK {
			t.Fatalf("код ответа на чтение = %d, ожидался %d", read.StatusCode, http.StatusOK)
		}

		// Отмена останавливает сервер, и это штатное завершение, а не отказ.
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("run(...) вернул ошибку: %v", err)
		}
		if !strings.Contains(out.String(), "остановлен") {
			t.Errorf("сервер не сообщил об остановке:\n%s", out.String())
		}
	})

	t.Run("занятый адрес — ошибка, а не паника", func(t *testing.T) {
		t.Parallel()

		busy, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen(...) вернул ошибку: %v", err)
		}
		defer busy.Close()

		var out bytes.Buffer

		if err := run(t.Context(), &out, busy.Addr().String()); err == nil {
			t.Fatal("run(...) на занятом адресе вернул nil")
		}
		if out.Len() != 0 {
			t.Errorf("несостоявшийся запуск что-то напечатал:\n%s", out.String())
		}
	})
}

// syncBuffer — вывод, в который пишет сервер, а читает тест.
//
// Горутины разные, поэтому обычный bytes.Buffer здесь ловится -race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// waitAddr дожидается напечатанного адреса и достаёт его.
//
// Порт выбирает ядро (:0), и узнать его можно только из вывода — то есть
// оттуда же, откуда его узнаёт человек, запустивший команду.
func waitAddr(t *testing.T, out *syncBuffer) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, rest, ok := strings.Cut(out.String(), "http://"); ok {
			if addr, _, ok := strings.Cut(rest, "\n"); ok {
				return strings.TrimSpace(addr)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("сервер не напечатал адрес за отведённое время:\n%s", out.String())
	return ""
}

// request готовит запрос с заголовком владельца: аутентификации нет, клиент
// называет себя сам.
func request(t *testing.T, method, url, body string) *http.Request {
	t.Helper()

	// Именно io.Reader, а не *strings.Reader: нулевой указатель в интерфейсе
	// сам по себе не nil, и NewRequest полез бы читать из него.
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("http.NewRequest(%s, %s) вернул ошибку: %v", method, url, err)
	}
	req.Header.Set("X-Owner-ID", "user-42")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// do выполняет запрос к поднятому серверу и закрывает тело ответа.
func do(t *testing.T, req *http.Request) *http.Response {
	t.Helper()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s вернул ошибку: %v", req.Method, req.URL, err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	return resp
}
