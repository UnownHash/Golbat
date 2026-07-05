ALTER TABLE incident DROP INDEX ix_expiration;

ALTER TABLE tappable DROP INDEX ix_expire_timestamp;

ALTER TABLE pokestop
    DROP INDEX ix_quest_expiry,
    DROP INDEX ix_alternative_quest_expiry;
