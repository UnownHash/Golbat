ALTER TABLE `raid_stats`
    ADD COLUMN fence varchar(40) NOT NULL DEFAULT '' AFTER `date`,
    ADD COLUMN area varchar(40) NOT NULL DEFAULT '' AFTER `date`,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (`date`, area, fence, pokemon_id),
    CHANGE `level` `level` SMALLINT UNSIGNED NULL DEFAULT NULL AFTER `fence`;

ALTER TABLE `invasion_stats`
    ADD COLUMN fence varchar(40) NOT NULL DEFAULT '' AFTER `date`,
    ADD COLUMN area varchar(40) NOT NULL DEFAULT '' AFTER `date`,
    CHANGE `grunt_type` `character` SMALLINT UNSIGNED NOT NULL,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (`date`, area, fence, `character`);