// Package pgtest поднимает базы под тесты, которым нужен живой Postgres.
//
// Пакет обычный, а не тестовый: его импортируют тесты двух разных пакетов —
// хранилища и команды, — а из файлов с суффиксом _test импортировать нечего.
// Так же устроены apptest в этом проекте и httptest в стандартной библиотеке.
//
// Схема накатывается один раз в шаблонную базу, а каждый тест получает свою
// копию через CREATE DATABASE ... TEMPLATE: копия готовой схемы обходится
// дешевле повторного наката миграций, а изоляция получается полной — до
// уровня параллельных записей, которые набор контракта проверяет всерьёз.
package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/deliseev/todoer/internal/infra/postgres"
)

// Переменные окружения.
//
// DSN тестов — отдельная переменная, не та, что у приложения: тесты создают и
// удаляют базы целиком, и промах в переменной не должен стоить рабочих данных.
//
// CIEnv выставляет сам GitHub Actions. Без базы тесты пропускаются — на машине
// разработчика, у которого не поднят контейнер, это нормально; в CI пропуск
// недопустим: зелёная сборка, ничего не проверившая, хуже красной.
const (
	DSNEnv = "TODOER_TEST_DATABASE_URL"
	CIEnv  = "CI"
)

var (
	// adminDSN — подключение, от имени которого создаются и удаляются базы.
	adminDSN string
	// templateName — имя шаблонной базы с накатанной схемой.
	templateName string
	// counter разводит имена баз внутри одного прогона.
	counter atomic.Int64
)

// Run готовит шаблонную базу, прогоняет тесты пакета и убирает за собой.
//
// Вызывается из TestMain и сам завершает процесс: возвращать управление
// после os.Exit некуда, а без os.Exit не отработает уборка шаблона.
func Run(m *testing.M) {
	adminDSN = strings.TrimSpace(os.Getenv(DSNEnv))

	// Без базы шаблон не нужен: тесты, которым она требуется, пропустятся
	// или упадут сами — по обстоятельствам, известным им, а не этой функции.
	if adminDSN == "" {
		os.Exit(m.Run())
	}

	templateName = uniqueName("todoer_tmpl")

	if err := createTemplate(); err != nil {
		// База могла быть уже создана, а споткнуться накат миграций: уйти,
		// не убрав её, значит оставить мусор на сервере до конца дней —
		// имя несёт номер процесса и второй раз само не встретится.
		if dropErr := dropDatabase(context.Background(), templateName); dropErr != nil {
			fmt.Fprintln(os.Stderr, "pgtest: удаление недоделанного шаблона:", dropErr)
		}
		fmt.Fprintln(os.Stderr, "pgtest: подготовка шаблонной базы:", err)
		os.Exit(1)
	}

	code := m.Run()

	// Шаблон снимается независимо от исхода: оставленная база переживёт
	// прогон и достанется следующему.
	if err := dropDatabase(context.Background(), templateName); err != nil {
		fmt.Fprintln(os.Stderr, "pgtest: удаление шаблонной базы:", err)
		os.Exit(1)
	}

	os.Exit(code)
}

// NewDSN отдаёт строку подключения к свежей базе с накатанной схемой.
// База удаляется по завершении теста.
func NewDSN(t *testing.T) string {
	t.Helper()

	return newDatabase(t, "CREATE DATABASE %s TEMPLATE "+quote(templateName))
}

// NewEmptyDSN отдаёт строку подключения к базе без схемы.
//
// Нужна тем, кто проверяет сами миграции: накатить их на базу, где они уже
// накатаны, — это проверка идемпотентности, а не наката.
func NewEmptyDSN(t *testing.T) string {
	t.Helper()

	return newDatabase(t, "CREATE DATABASE %s")
}

// newDatabase создаёт базу по заданному шаблону команды и убирает её за собой.
//
// Нет базы — тест пропускается, но только не в CI: там её отсутствие означает
// сломанное окружение, а не выбор разработчика.
func newDatabase(t *testing.T, command string) string {
	t.Helper()

	if adminDSN == "" {
		if os.Getenv(CIEnv) != "" {
			t.Fatalf("%s не задана: в CI интеграционные тесты обязаны выполняться, а не пропускаться", DSNEnv)
		}
		t.Skipf("%s не задана: поднимите базу через docker compose up -d --wait db", DSNEnv)
	}

	name := uniqueName("todoer_test")

	if err := exec(t.Context(), fmt.Sprintf(command, quote(name))); err != nil {
		t.Fatalf("pgtest: создание базы теста: %v", err)
	}

	t.Cleanup(func() {
		// Контекст теста к этому моменту уже отменён, поэтому берётся свой.
		if err := dropDatabase(context.Background(), name); err != nil {
			t.Errorf("pgtest: удаление базы теста: %v", err)
		}
	})

	return withDatabase(adminDSN, name)
}

// createTemplate создаёт шаблонную базу и накатывает в неё схему.
func createTemplate() error {
	ctx := context.Background()

	// IF EXISTS, а не молчаливое пересоздание: имя несёт номер процесса и
	// столкнуться может только с базой, оставшейся от убитого прогона.
	if err := dropDatabase(ctx, templateName); err != nil {
		return err
	}
	if err := exec(ctx, "CREATE DATABASE "+quote(templateName)); err != nil {
		return err
	}

	// Схема накатывается тем же кодом, что и в бою: тесты не имеют права
	// знать о схеме больше, чем знает приложение.
	if err := postgres.Migrate(ctx, withDatabase(adminDSN, templateName)); err != nil {
		return fmt.Errorf("накат миграций в шаблон: %w", err)
	}

	return nil
}

// dropDatabase удаляет базу вместе с оставшимися подключениями.
//
// FORCE отцепляет то, что осталось подключённым: пул закрывается раньше,
// в своём t.Cleanup, но порядок между уборщиками лучше не угадывать —
// незакрытое соединение иначе оставит базу навсегда.
func dropDatabase(ctx context.Context, name string) error {
	return exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", quote(name)))
}

// exec выполняет команду на административном соединении.
//
// Соединение открывается и закрывается на каждую команду: CREATE DATABASE
// с шаблоном не выполняется, пока к шаблону кто-то подключён, а долгоживущее
// соединение оснастки — первый кандидат в такие «кто-то».
func exec(ctx context.Context, sql string) error {
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("подключение к базе: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("выполнение %q: %w", sql, err)
	}

	return nil
}

// uniqueName собирает имя базы, не совпадающее ни с чьим другим: номер
// процесса разводит одновременные прогоны, счётчик — тесты внутри прогона.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, os.Getpid(), counter.Add(1))
}

// withDatabase подменяет имя базы в строке подключения.
func withDatabase(dsn, name string) string {
	parsed, err := url.Parse(dsn)
	if err != nil {
		// Строка уже была разобрана при подключении, поэтому сюда не попасть.
		panic("pgtest: неразбираемая строка подключения: " + err.Error())
	}

	parsed.Path = "/" + name
	return parsed.String()
}

// quote заключает имя в кавычки по правилам Postgres.
//
// Имена собираются здесь же и из безобидных частей, но экранирование — не про
// этот случай, а про привычку: подстановка имени в SQL конкатенацией один раз
// оказывается безопасной, а на второй уже нет.
func quote(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
