-- 001_queue.up.sql (SQLite)
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
-- Every timestamp is BIGINT UnixNano, never a native date type, and no TEXT
-- column carries a constant DEFAULT — the Go code binds every column on INSERT.
-- That keeps the MySQL twin from having to promote payload-sized columns to
-- VARCHAR(n), which would put a length ceiling on one dialect only.

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
-- schedule_id carries no foreign key. This template does not set
-- PRAGMA foreign_keys=ON, so an inline REFERENCES would be enforced on
-- MySQL/PostgreSQL and ignored here — the dialect divergence migrations/README.md
-- warns about. The index is what the queries need anyway.
--
-- Considered and rejected: merging run_at and lease_until into one next_at
-- column (pending → scheduled time, running → lease expiry) to save an index.
-- The task detail screen shows both at once, and a merged column loses the
-- original run_at the moment the task starts.
CREATE TABLE IF NOT EXISTS queue_tasks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    kind         TEXT    NOT NULL,
    payload      TEXT    NOT NULL,
    status       TEXT    NOT NULL,
    priority     INTEGER NOT NULL,
    run_at       BIGINT  NOT NULL,
    attempts     INTEGER NOT NULL,
    max_attempts INTEGER NOT NULL,
    timeout_ms   INTEGER NOT NULL,
    lease_until  BIGINT  NOT NULL,
    lease_token  TEXT    NOT NULL,
    worker       TEXT    NOT NULL,
    schedule_id  BIGINT,
    unique_key   TEXT,
    last_error   TEXT    NOT NULL,
    created_at   BIGINT  NOT NULL,
    updated_at   BIGINT  NOT NULL,
    started_at   BIGINT  NOT NULL,
    finished_at  BIGINT  NOT NULL
);

-- The claim scan. priority is deliberately absent: putting it between status and
-- run_at would destroy the range scan on run_at, which is what narrows the set in
-- the first place. Ordering by priority happens on the already-narrow result.
CREATE INDEX IF NOT EXISTS queue_tasks_claim ON queue_tasks (status, run_at);

-- The reaper scan: running rows whose lease has expired.
CREATE INDEX IF NOT EXISTS queue_tasks_lease ON queue_tasks (status, lease_until);

-- The pruner scan: terminal rows older than the retention window.
CREATE INDEX IF NOT EXISTS queue_tasks_finished ON queue_tasks (status, finished_at);

-- One cron plan's execution history.
CREATE INDEX IF NOT EXISTS queue_tasks_schedule ON queue_tasks (schedule_id, id);

-- Idempotency. All three dialects allow many NULLs in a unique index, so a task
-- without a key is simply not deduplicated. Note the uniqueness is PERMANENT,
-- not "unique among non-terminal rows" — MySQL has no partial index, so that
-- could not be expressed portably. Callers wanting periodic deduplication put a
-- time bucket in the key, which is what the cron producer does.
CREATE UNIQUE INDEX IF NOT EXISTS queue_tasks_unique_key ON queue_tasks (unique_key);

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
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     BIGINT  NOT NULL,
    attempt     INTEGER NOT NULL,
    outcome     TEXT    NOT NULL,
    worker      TEXT    NOT NULL,
    error       TEXT    NOT NULL,
    started_at  BIGINT  NOT NULL,
    finished_at BIGINT  NOT NULL
);

CREATE INDEX IF NOT EXISTS queue_attempts_task ON queue_attempts (task_id, id);

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
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL UNIQUE,
    kind         TEXT    NOT NULL,
    payload      TEXT    NOT NULL,
    spec         TEXT    NOT NULL,
    code_spec    TEXT    NOT NULL,
    enabled      INTEGER NOT NULL,
    present      INTEGER NOT NULL,
    next_run_at  BIGINT  NOT NULL,
    last_fire_at BIGINT  NOT NULL,
    last_task_id BIGINT  NOT NULL,
    max_attempts INTEGER NOT NULL,
    created_at   BIGINT  NOT NULL,
    updated_at   BIGINT  NOT NULL
);

-- The due scan, and the only index the scheduler needs.
CREATE INDEX IF NOT EXISTS queue_schedules_due ON queue_schedules (enabled, next_run_at);
