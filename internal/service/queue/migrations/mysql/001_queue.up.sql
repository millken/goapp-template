-- 001_queue.up.sql (MySQL)
-- The task queue: one table of work, one of finished attempts, one of cron plans.
--
-- Numbered from 001 because these migrations are a history of their own. They
-- are applied under the "queue" migration service (see ../../migrations.go), so
-- the version recorded for them is independent of the application's own. That is
-- what lets the queue component be added to a project whose schema has already
-- moved past this number: the migrator keeps a single high-water mark per
-- service, so a shared numbering would silently skip this file forever.
--
-- Three tables rather than one because "periodic" is a property of the producer,
-- not of the task. queue_schedules produces; queue_tasks only ever knows a
-- run_at. So one-shot (run_at = now), delayed (run_at = now + d) and periodic
-- (a row inserted by the scheduler each time it fires) share one executor, one
-- retry policy and one failure log.
--
-- Two MySQL-specific shapes, both from migrations/README.md:
--
--   * Every index is declared INSIDE CREATE TABLE. MySQL has no
--     CREATE INDEX IF NOT EXISTS, so a separate statement would not be
--     re-runnable.
--   * Indexed and fixed-vocabulary strings are VARCHAR(n), because MySQL cannot
--     index TEXT without a prefix length.
--
-- What is NOT promoted to VARCHAR: payload, error and last_error stay TEXT.
-- That works here only because no TEXT column carries a constant DEFAULT — the
-- Go code binds every column on INSERT — so this dialect gains no length ceiling
-- the other two lack.
--
-- Every timestamp is BIGINT UnixNano; INTEGER is 32-bit here and would overflow
-- 2.1 seconds after 1970.

-- status is one of: pending | running | succeeded | dead | cancelled
--
-- There is deliberately no 'failed'. A failure that will be retried goes back to
-- 'pending' with run_at pushed out, so the only terminal failure is 'dead'.
-- Calling it 'failed' would read as "the state after any failure", and the
-- direct product of that misreading is an admin screen whose failure filter is
-- empty while retries are in flight.
--
-- attempts is incremented when a task is CLAIMED, not when it finishes. That one
-- decision is the whole mechanism by which a task that kills the process (OOM,
-- a cgo segfault) still burns an attempt and eventually lands in 'dead' instead
-- of retrying forever, one dead worker at a time.
--
-- lease_token is its own random column rather than reusing lease_until as the
-- fencing token: "two workers cannot compute the same UnixNano" is exactly the
-- kind of assumption that breaks on a coarse clock.
--
-- schedule_id carries no foreign key. MySQL would enforce one, SQLite would not
-- (this template does not set PRAGMA foreign_keys=ON) — the dialect divergence
-- migrations/README.md warns about. The index is what the queries need anyway.
--
-- unique_key is VARCHAR(191): the safe single-column unique-index length under
-- utf8mb4. All three dialects allow many NULLs in a unique index, so a task
-- without a key is simply not deduplicated. Uniqueness is PERMANENT, not "unique
-- among non-terminal rows" — MySQL has no partial index, so that could not be
-- expressed portably. Callers wanting periodic deduplication put a time bucket in
-- the key, which is what the cron producer does.
CREATE TABLE IF NOT EXISTS queue_tasks (
    id           BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    kind         VARCHAR(64)  NOT NULL,
    payload      TEXT         NOT NULL,
    status       VARCHAR(16)  NOT NULL,
    priority     INT          NOT NULL,
    run_at       BIGINT       NOT NULL,
    attempts     INT          NOT NULL,
    max_attempts INT          NOT NULL,
    timeout_ms   INT          NOT NULL,
    lease_until  BIGINT       NOT NULL,
    lease_token  VARCHAR(32)  NOT NULL,
    worker       VARCHAR(128) NOT NULL,
    schedule_id  BIGINT,
    unique_key   VARCHAR(191),
    last_error   TEXT         NOT NULL,
    created_at   BIGINT       NOT NULL,
    updated_at   BIGINT       NOT NULL,
    started_at   BIGINT       NOT NULL,
    finished_at  BIGINT       NOT NULL,
    -- The claim scan. priority is deliberately absent: putting it between status
    -- and run_at would destroy the range scan on run_at, which is what narrows
    -- the set in the first place. Ordering by priority happens on the
    -- already-narrow result.
    INDEX queue_tasks_claim (status, run_at),
    -- The reaper scan: running rows whose lease has expired.
    INDEX queue_tasks_lease (status, lease_until),
    -- The pruner scan: terminal rows older than the retention window.
    INDEX queue_tasks_finished (status, finished_at),
    -- One cron plan's execution history.
    INDEX queue_tasks_schedule (schedule_id, id),
    UNIQUE KEY queue_tasks_unique_key (unique_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- One row per FINISHED attempt — this table is the failure log the admin screen
-- reads. Nothing is written at claim time: claim is the hot path, and a
-- placeholder row would cost an INSERT there plus an UPDATE later. The gap left
-- by a process that died mid-attempt is filled by the reaper, which writes the
-- row with outcome='lost' when it reclaims the lease. That also answers "does a
-- reclaim count as a failed attempt": it does, and it is recorded as 'lost'.
--
-- outcome is one of: succeeded | failed | timeout | lost | cancelled
-- duration is not stored; Go subtracts finished_at - started_at.
CREATE TABLE IF NOT EXISTS queue_attempts (
    id          BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    task_id     BIGINT       NOT NULL,
    attempt     INT          NOT NULL,
    outcome     VARCHAR(16)  NOT NULL,
    worker      VARCHAR(128) NOT NULL,
    error       TEXT         NOT NULL,
    started_at  BIGINT       NOT NULL,
    finished_at BIGINT       NOT NULL,
    INDEX queue_attempts_task (task_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Cron plans. Ownership of each column is the point of this table:
--
--   code owns  name, kind, payload, code_spec   (overwritten on every sync)
--   ops  owns  spec, enabled                    (sync never clobbers them)
--   system owns next_run_at, last_fire_at, last_task_id
--
-- spec and code_spec together make startup sync a three-way merge: code_spec is
-- the base (what the code said last time), spec is ours (what is in effect), and
-- the newly registered expression is theirs. If spec == code_spec nobody touched
-- it and it follows the code; otherwise ops edited it and only code_spec moves.
-- "Drifted" is therefore derived from spec != code_spec — no extra flag column.
--
-- present is 0 for a plan whose name is no longer registered in the code. Such a
-- row is never fired, but it is NOT deleted: deleting would throw away an
-- operator's expression edit and its enabled state over one deployment that
-- happened to be missing a handler, and would orphan the schedule_id of every
-- task it ever produced.
--
-- No rows are seeded here. A plan can only come from code, which is the whole of
-- "the admin area cannot invent a task with no handler".
CREATE TABLE IF NOT EXISTS queue_schedules (
    id           BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name         VARCHAR(64)  NOT NULL,
    kind         VARCHAR(64)  NOT NULL,
    payload      TEXT         NOT NULL,
    spec         VARCHAR(128) NOT NULL,
    code_spec    VARCHAR(128) NOT NULL,
    enabled      SMALLINT     NOT NULL,
    present      SMALLINT     NOT NULL,
    next_run_at  BIGINT       NOT NULL,
    last_fire_at BIGINT       NOT NULL,
    last_task_id BIGINT       NOT NULL,
    max_attempts INT          NOT NULL,
    created_at   BIGINT       NOT NULL,
    updated_at   BIGINT       NOT NULL,
    UNIQUE KEY queue_schedules_name (name),
    -- The due scan, and the only index the scheduler needs.
    INDEX queue_schedules_due (enabled, next_run_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
