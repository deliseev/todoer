-- +goose Up

-- Ранг важности приоритета — служебный ключ сортировки списка.
--
-- Сортировать по самому priority нельзя: это имя, и алфавит ставит critical
-- раньше high. Расписать порядок важности выражением в запросе — тоже: у
-- правила стал бы второй носитель, который разошёлся бы с доменом молча.
-- Поэтому хранилище пишет сюда ранг, взятый у todo.Priority.Rank(), и колонка
-- живёт как копия: она пишется, но никогда не читается, а приоритет
-- поднимается из имени. Плата за копию названа прямо: смена рангов в домене
-- потребует новой миграции с такой же засыпкой.
ALTER TABLE tasks ADD COLUMN priority_rank smallint;

-- Засыпка — единственное место, где порядок важности переписан на SQL, и это
-- допустимо ровно потому, что миграция не правило, а снимок: она выполняется
-- один раз и описывает прошлое. Ранги начинаются с единицы — ноль означал бы
-- «ранга нет».
UPDATE tasks SET priority_rank = CASE priority
    WHEN 'low'      THEN 1
    WHEN 'normal'   THEN 2
    WHEN 'high'     THEN 3
    WHEN 'critical' THEN 4
END;

ALTER TABLE tasks ALTER COLUMN priority_rank SET NOT NULL;

-- Индекс по владельцу, обещанный в 00001, приезжает сюда — тремя штуками,
-- по одному на порядок сортировки списка. Это прямая цена трёх сортировок:
-- без своего индекса keyset-страница по приоритету или сроку перебирала бы
-- всё, ради чего курсор и выбирался вместо OFFSET.
--
-- Владелец первым в каждом ключе: чужие задачи в выдачу не попадают никогда,
-- поэтому отбор по нему идёт раньше любого другого.
--
-- coalesce(due_date, 'infinity') вместо голого due_date: NULL ломает и
-- сравнение кортежей курсора, и порядок в конце — а 'infinity' даёт задачам
-- без срока определённое место и сравнимое значение.
CREATE INDEX tasks_owner_due_idx ON tasks (owner_id, coalesce(due_date, 'infinity'::timestamptz), id);

CREATE INDEX tasks_owner_priority_idx ON tasks (owner_id, priority_rank, id);

CREATE INDEX tasks_owner_created_idx ON tasks (owner_id, created_at, id);

-- +goose Down

DROP INDEX tasks_owner_created_idx;

DROP INDEX tasks_owner_priority_idx;

DROP INDEX tasks_owner_due_idx;

ALTER TABLE tasks DROP COLUMN priority_rank;
