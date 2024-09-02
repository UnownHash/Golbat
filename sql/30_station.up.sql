CREATE TABLE `station` (
 `id`                       VARCHAR(35) NOT NULL,
 `lat`                      DOUBLE(18,14) NOT NULL,
 `lon`                      DOUBLE(18,14) NOT NULL,
 `name`                     VARCHAR(128) NOT NULL,
 `cell_id`                  BIGINT NOT NULL,
 `start_time`               INT UNSIGNED NOT NULL,
 `end_time`                 INT UNSIGNED NOT NULL,
 `cooldown_complete`        INT UNSIGNED NOT NULL,
 `is_battle_available`      TINYINT UNSIGNED NOT NULL,
 `is_inactive`              TINYINT UNSIGNED NOT NULL,
 `battle_level`             TINYINT UNSIGNED DEFAULT NULL,
 `battle_pokemon_id`        smallint unsigned,
 `battle_pokemon_form`      smallint unsigned,
 `battle_pokemon_costume`   smallint unsigned,
 `battle_pokemon_gender`    tinyint unsigned,
 `battle_pokemon_alignment` smallint unsigned,
 `updated`                  INT UNSIGNED NOT NULL,
 PRIMARY KEY(`id`),
 KEY `ix_coords` (`lat`,`lon`),
 KEY `ix_end_time` (`end_time`),
 KEY `ix_updated` (`updated`),
 KEY `ix_battle_pokemon_id` (`battle_pokemon_id`),
 KEY `fk_station_cell_id` (`cell_id`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_general_ci;
