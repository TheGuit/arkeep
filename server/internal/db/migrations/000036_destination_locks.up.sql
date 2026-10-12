-- Destination locks (issue #319) replace the single-holder busy gate
-- (destinations.busy_job_id, issue #130). restic takes a non-exclusive lock for
-- a backup, so any number of backups may run against one repository at once; a
-- retention sweep (forget --prune) or an integrity check needs it alone. Each
-- row is one job's hold on one destination: mode 'shared' for a backup,
-- 'exclusive' for retention and check. Deleting a job (job-log retention)
-- drops its holds with it.
CREATE TABLE IF NOT EXISTS destination_locks (
    destination_id  TEXT      NOT NULL,
    job_id          TEXT      NOT NULL,
    mode            TEXT      NOT NULL,
    acquired_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (destination_id, job_id),
    CONSTRAINT fk_destination_locks_destination FOREIGN KEY (destination_id) REFERENCES destinations (id) ON DELETE CASCADE,
    CONSTRAINT fk_destination_locks_job         FOREIGN KEY (job_id)         REFERENCES jobs         (id) ON DELETE CASCADE,
    CONSTRAINT destination_locks_mode_check     CHECK (mode IN ('shared', 'exclusive'))
);
CREATE INDEX IF NOT EXISTS idx_destination_locks_job_id ON destination_locks (job_id);

-- Carry over the holds still in effect.
INSERT INTO destination_locks (destination_id, job_id, mode, acquired_at)
SELECT d.id, j.id,
       CASE WHEN j.type = 'backup' THEN 'shared' ELSE 'exclusive' END,
       COALESCE(d.busy_since, CURRENT_TIMESTAMP)
FROM destinations d
INNER JOIN jobs j ON j.id = d.busy_job_id
WHERE j.status IN ('pending', 'waiting', 'running');

ALTER TABLE destinations DROP COLUMN busy_job_id;
ALTER TABLE destinations DROP COLUMN busy_since;
