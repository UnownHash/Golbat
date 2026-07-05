-- The hourly cleanup jobs were full-scanning these tables; for the DELETEs
-- InnoDB also takes locks on rows as it examines them, so each pass
-- contended with live ingest (incident writes especially) and churned the
-- buffer pool. These indexes let each job touch only the expired rows.
-- IF NOT EXISTS (MariaDB extension): live databases in this ecosystem often
-- carry operator-added indexes with these exact names.

ALTER TABLE incident
    ADD INDEX IF NOT EXISTS ix_expiration (expiration);

ALTER TABLE tappable
    ADD INDEX IF NOT EXISTS ix_expire_timestamp (expire_timestamp);

ALTER TABLE pokestop
    ADD INDEX IF NOT EXISTS ix_quest_expiry (quest_expiry),
    ADD INDEX IF NOT EXISTS ix_alternative_quest_expiry (alternative_quest_expiry);
