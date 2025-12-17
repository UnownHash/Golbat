CREATE TABLE IF NOT EXISTS `invasion_lineup_stats` (
    `date` date NOT NULL,
    `area` varchar(255) NOT NULL DEFAULT '',
    `fence` varchar(255) NOT NULL DEFAULT '',
    `character` smallint unsigned NOT NULL,
    `slot` tinyint unsigned NOT NULL,
    `pokemon_id` smallint unsigned NOT NULL,
    `form_id` smallint unsigned NOT NULL DEFAULT 0,
    `count` int NOT NULL,
    PRIMARY KEY (`date`, `area`, `fence`, `character`, `slot`, `pokemon_id`, `form_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
