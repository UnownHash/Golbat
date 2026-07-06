ALTER TABLE gym
    ADD `raid_seed` BIGINT DEFAULT NULL AFTER `raid_battle_timestamp`,
    ADD `raid_pokemon_stamina` INT DEFAULT NULL AFTER `raid_pokemon_cp`,
    ADD `raid_pokemon_cp_multiplier` FLOAT DEFAULT NULL AFTER `raid_pokemon_stamina`;

ALTER TABLE raid_stats
    ADD COLUMN alignment SMALLINT UNSIGNED NOT NULL DEFAULT 0 AFTER temp_evo_id;

ALTER TABLE raid_stats
    DROP PRIMARY KEY;

ALTER TABLE raid_stats
    ADD PRIMARY KEY (date, area, fence, pokemon_id, form_id, temp_evo_id, alignment, level);
