ALTER TABLE raid_stats
    ADD COLUMN form_id SMALLINT UNSIGNED NOT NULL DEFAULT 0 AFTER pokemon_id;

ALTER TABLE raid_stats
    DROP PRIMARY KEY;

ALTER TABLE raid_stats
    ADD PRIMARY KEY (pokemon_id, date, level, form_id);
