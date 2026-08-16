// Package postgres содержит реализацию порта app.Repository поверх Postgres,
// схему базы и её миграции.
//
// Пакет — часть инфраструктуры и стоит на внешней границе: он знает про app
// и todo, обратного знания нет. Внешние зависимости — драйвер и мигратор —
// живут здесь и в cmd, и никуда глубже не проникают: домен и сценарии
// по-прежнему обходятся стандартной библиотекой.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// migrationsDir — каталог с миграциями внутри вшитой файловой системы.
const migrationsDir = "migrations"

// migrationsFS — миграции, вшитые в бинарник.
//
// Вшиты, а не прочитаны с диска: команда, накатывающая схему, обязана работать
// там, где рядом с ней нет исходников, — на машине выкатки лежит один файл.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// newProvider собирает мигратор goose поверх соединения с базой.
//
// goose работает через database/sql, а боевые запросы идут мимо него — родным
// API pgx. Второй зависимости это не стоит: stdlib лежит в том же модуле, что
// и pgx, и переводит его в интерфейс database/sql.
func newProvider(db *sql.DB) (*goose.Provider, error) {
	// goose ищет миграции в корне переданной файловой системы, поэтому от
	// вшитого дерева отрезается каталог.
	dir, err := fs.Sub(migrationsFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("postgres: open embedded migrations: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, dir)
	if err != nil {
		return nil, fmt.Errorf("postgres: build migration provider: %w", err)
	}

	return provider, nil
}

// openSQLDB открывает соединение database/sql для мигратора.
func openSQLDB(dsn string) (*sql.DB, error) {
	config, err := parseConfig(dsn)
	if err != nil {
		return nil, err
	}

	// Мигратору достаётся настройка одного соединения из той же конфигурации,
	// что и боевому пулу: сроки у них общие.
	return stdlib.OpenDB(*config.ConnConfig), nil
}

// withProvider выполняет работу над мигратором и закрывает соединение за собой.
func withProvider(dsn string, work func(*goose.Provider) error) error {
	db, err := openSQLDB(dsn)
	if err != nil {
		return err
	}
	// Соединение мигратора обязано закрыться: пока оно живо, базу нельзя
	// использовать как шаблон для CREATE DATABASE, а именно так поднимаются
	// базы под тесты.
	defer db.Close()

	provider, err := newProvider(db)
	if err != nil {
		return err
	}

	return work(provider)
}

// Migrate накатывает все недостающие миграции.
//
// Приложение этого не делает: схему двигает выкатка, отдельным шагом. Молчащий
// goose.Up на старте означал бы, что при выкатке двух экземпляров схему
// одновременно двигают оба.
func Migrate(ctx context.Context, dsn string) error {
	return withProvider(dsn, func(provider *goose.Provider) error {
		if _, err := provider.Up(ctx); err != nil {
			return fmt.Errorf("postgres: apply migrations: %w", err)
		}
		return nil
	})
}

// Rollback откатывает последнюю применённую миграцию.
func Rollback(ctx context.Context, dsn string) error {
	return withProvider(dsn, func(provider *goose.Provider) error {
		if _, err := provider.Down(ctx); err != nil {
			return fmt.Errorf("postgres: roll back migration: %w", err)
		}
		return nil
	})
}

// MigrationStatus — состояние одной миграции: применена ли и когда.
type MigrationStatus struct {
	Version int64
	Source  string
	Applied bool
}

// Status сообщает состояние каждой миграции в базе.
func Status(ctx context.Context, dsn string) ([]MigrationStatus, error) {
	var statuses []MigrationStatus

	err := withProvider(dsn, func(provider *goose.Provider) error {
		results, err := provider.Status(ctx)
		if err != nil {
			return fmt.Errorf("postgres: read migration status: %w", err)
		}

		statuses = make([]MigrationStatus, 0, len(results))
		for _, result := range results {
			statuses = append(statuses, MigrationStatus{
				Version: result.Source.Version,
				Source:  result.Source.Path,
				Applied: !result.AppliedAt.IsZero(),
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return statuses, nil
}

// SchemaVersion сообщает версию схемы, накатанной в базе.
func SchemaVersion(ctx context.Context, dsn string) (int64, error) {
	var version int64

	err := withProvider(dsn, func(provider *goose.Provider) error {
		got, err := provider.GetDBVersion(ctx)
		if err != nil {
			return fmt.Errorf("postgres: read schema version: %w", err)
		}
		version = got
		return nil
	})
	if err != nil {
		return 0, err
	}

	return version, nil
}

// ExpectedSchemaVersion возвращает версию схемы, на которую рассчитан этот код:
// наибольшую из вшитых миграций.
//
// Приложение сверяется с ней при старте. Отставшая база — это код, который
// обращается к колонкам, которых ещё нет: лучше не подняться совсем, чем
// подниматься и падать на каждом запросе.
// Читается прямо из вшитых файлов, а не через мигратор: тому нужно соединение
// с базой, а вопрос «на какую схему рассчитан этот бинарник» отвечается без
// неё — и обязан отвечать, иначе проверка при старте зависела бы от того,
// что проверяет.
func ExpectedSchemaVersion() (int64, error) {
	names, err := fs.Glob(migrationsFS, migrationsDir+"/*.sql")
	if err != nil {
		return 0, fmt.Errorf("postgres: list embedded migrations: %w", err)
	}

	var latest int64
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return 0, err
		}
		if version > latest {
			latest = version
		}
	}

	// Пустой список означает, что вшивание сломалось: файлы переименовали или
	// перенесли, директива //go:embed осталась прежней. Молчать об этом нельзя
	// — приложение решило бы, что схемы ему не нужно вовсе.
	if latest == 0 {
		return 0, fmt.Errorf("postgres: read embedded migrations (nothing matched %s/*.sql)", migrationsDir)
	}

	return latest, nil
}

// migrationVersion достаёт версию из имени файла миграции: goose держит её
// в начале имени, до первого подчёркивания.
func migrationVersion(name string) (int64, error) {
	base := path.Base(name)

	digits, _, found := strings.Cut(base, "_")
	if !found {
		return 0, fmt.Errorf("postgres: parse migration name %s (no version before the first underscore)", base)
	}

	version, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		// Причина приезжает от strconv: она называет, что именно оказалось
		// не числом, и в логе это полезнее собственной формулировки.
		return 0, fmt.Errorf("postgres: parse migration name %s: %w", base, err)
	}

	return version, nil
}
