ALTER TABLE destinations ADD COLUMN busy_job_id TEXT;
ALTER TABLE destinations ADD COLUMN busy_since TIMESTAMP;

-- The single-holder gate keeps one of the current holders: the oldest.
UPDATE destinations SET
    busy_job_id = (SELECT l.job_id FROM destination_locks l WHERE l.destination_id = destinations.id ORDER BY l.acquired_at, l.job_id LIMIT 1),
    busy_since  = (SELECT l.acquired_at FROM destination_locks l WHERE l.destination_id = destinations.id ORDER BY l.acquired_at, l.job_id LIMIT 1);

DROP TABLE IF EXISTS destination_locks;
