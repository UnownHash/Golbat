ALTER TABLE `raid_stats`
    ADD COLUMN fence varchar(255) NOT NULL DEFAULT '' AFTER `date`,
    ADD COLUMN area varchar(255) NOT NULL DEFAULT '' AFTER `date`,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (`date`, area, fence, pokemon_id),
    CHANGE `level` `level` SMALLINT UNSIGNED NULL DEFAULT NULL AFTER `fence`;

ALTER TABLE `invasion_stats`
    ADD COLUMN fence varchar(255) NOT NULL DEFAULT '' AFTER `date`,
    ADD COLUMN area varchar(255) NOT NULL DEFAULT '' AFTER `date`,
    CHANGE `grunt_type` `character` SMALLINT UNSIGNED NOT NULL,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (`date`, area, fence, `character`);

ALTER TABLE `quest_stats`
    ADD COLUMN fence varchar(255) NOT NULL DEFAULT '' AFTER `date`,
    ADD COLUMN area varchar(255) NOT NULL DEFAULT '' AFTER `date`,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (`date`, area, fence, reward_type, pokemon_id, item_id);

ALTER TABLE pokemon_iv_stats
    MODIFY area varchar(255) NOT NULL DEFAULT '' AFTER `date`,
    MODIFY fence varchar(255) NOT NULL DEFAULT '' AFTER area;

ALTER TABLE pokemon_hundo_stats
    MODIFY area varchar(255) NOT NULL DEFAULT '' AFTER `date`,
    MODIFY fence varchar(255) NOT NULL DEFAULT '' AFTER area;

ALTER TABLE pokemon_shiny_stats
    MODIFY area varchar(255) NOT NULL DEFAULT '' AFTER `date`,
    MODIFY fence varchar(255) NOT NULL DEFAULT '' AFTER area;

ALTER TABLE pokemon_stats
    MODIFY area varchar(255) NOT NULL DEFAULT '' AFTER `date`,
    MODIFY fence varchar(255) NOT NULL DEFAULT '' AFTER area;

ALTER TABLE pokemon_area_stats
    MODIFY area varchar(255),
    MODIFY fence varchar(255);

ALTER TABLE pokemon_nundo_stats
    MODIFY area varchar(255),
    MODIFY fence varchar(255);