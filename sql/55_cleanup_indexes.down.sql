ALTER TABLE incident DROP INDEX IF EXISTS ix_expiration;

ALTER TABLE tappable DROP INDEX IF EXISTS ix_expire_timestamp;

ALTER TABLE pokestop
    DROP INDEX IF EXISTS ix_quest_expiry,
    DROP INDEX IF EXISTS ix_alternative_quest_expiry;
