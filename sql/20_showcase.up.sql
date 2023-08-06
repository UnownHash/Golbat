ALTER TABLE `pokestop`
    ADD COLUMN `showcase_pokemon_id` smallint unsigned DEFAULT NULL,
    ADD COLUMN `showcase_ranking_standard` tinyint(1) DEFAULT NULL,
    ADD COLUMN `showcase_expiry` int unsigned DEFAULT NULL,
    ADD COLUMN `showcase_rankings` text DEFAULT NULL;
