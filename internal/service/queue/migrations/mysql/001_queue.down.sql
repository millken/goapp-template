-- 001_queue.down.sql (MySQL)
-- Drops the queue with its tables; the indexes go with them.
--
-- Order is attempts → tasks → schedules, i.e. the reverse of the reference
-- direction (an attempt names a task, a task names a schedule). Nothing here
-- declares a foreign key, so the order is not enforced — it is written this way
-- so the file reads correctly against the dialects where it would be.
--
-- This discards queued work, its history and the operators' cron expression
-- edits. That is the right outcome for a down migration: rolling the queue back
-- means the component is gone, and half a queue is worse than none.
DROP TABLE IF EXISTS queue_attempts;
DROP TABLE IF EXISTS queue_tasks;
DROP TABLE IF EXISTS queue_schedules;
