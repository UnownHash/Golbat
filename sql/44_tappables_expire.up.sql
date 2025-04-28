ALTER TABLE tappable
    ADD `expire_timestamp` int unsigned DEFAULT NULL AFTER `count`,
    ADD `expire_timestamp_verified` tinyint unsigned NOT NULL AFTER `count`;

ALTER TABLE tappable
    add index `ix_expire_timestamp` (`expire_timestamp`, `expire_timestamp_verified`);
