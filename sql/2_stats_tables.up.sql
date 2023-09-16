CREATE TABLE `pokemon_history` (
   `id` varchar(25) NOT NULL,
   `location` point NOT NULL,
   `expire_timestamp` int unsigned DEFAULT NULL,
   `pokemon_id` smallint unsigned NOT NULL,
   `cp` smallint unsigned DEFAULT NULL,
   `atk_iv` tinyint unsigned DEFAULT NULL,
   `def_iv` tinyint unsigned DEFAULT NULL,
   `sta_iv` tinyint unsigned DEFAULT NULL,
   `form` smallint unsigned DEFAULT NULL,
   `level` tinyint unsigned DEFAULT NULL,
   `weather` tinyint unsigned DEFAULT NULL,
   `costume` tinyint unsigned DEFAULT NULL,
   `iv` float(5,2) unsigned GENERATED ALWAYS AS (((((`atk_iv` + `def_iv`) + `sta_iv`) * 100) / 45)) VIRTUAL,
    `cell_id` bigint unsigned DEFAULT NULL,
    `expire_timestamp_verified` tinyint unsigned NOT NULL,
    `display_pokemon_id` smallint unsigned DEFAULT NULL,
    `seen_type` enum('wild','encounter','nearby_stop','nearby_cell') DEFAULT NULL,
    `shiny` tinyint(1) DEFAULT '0',
    `seen_wild` int unsigned DEFAULT NULL,
    `seen_stop` int unsigned DEFAULT NULL,
    `seen_cell` int unsigned DEFAULT NULL,
    `seen_lure` int unsigned DEFAULT NULL,
    `first_encounter` int unsigned DEFAULT NULL,
    `stats_reset` int unsigned DEFAULT NULL,
    `last_encounter` int unsigned DEFAULT NULL,
    `lure_encounter` int unsigned DEFAULT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

ALTER TABLE `pokemon_history` ADD SPATIAL INDEX `location` (`location`);
ALTER TABLE `pokemon_history` ADD INDEX `first_encounter` (`first_encounter`);
ALTER TABLE `pokemon_history` ADD INDEX `seen_wild` (`seen_wild`);

CREATE TABLE `pokemon_timing` (
                                  `id` varchar(25) NOT NULL,
                                  `seen_wild` int unsigned DEFAULT NULL,
                                  `seen_stop` int unsigned DEFAULT NULL,
                                  `seen_cell` int unsigned DEFAULT NULL,
                                  `seen_lure` int unsigned DEFAULT NULL,
                                  `first_encounter` int unsigned DEFAULT NULL,
                                  `stats_reset` int unsigned DEFAULT NULL,
                                  `last_encounter` int unsigned DEFAULT NULL,
                                  `lure_encounter` int unsigned DEFAULT NULL,
                                  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

create procedure createStatsAndArchive()
begin
    drop temporary table if exists old;
    /* removed */
end;


