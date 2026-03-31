CREATE TABLE `station_battle` (
 `bread_battle_seed`         BIGINT NOT NULL,
 `station_id`                VARCHAR(35) NOT NULL,
 `battle_level`              TINYINT UNSIGNED NOT NULL,
 `battle_start`              INT UNSIGNED NOT NULL,
 `battle_end`                INT UNSIGNED NOT NULL,
 `battle_pokemon_id`         SMALLINT unsigned DEFAULT NULL,
 `battle_pokemon_form`       SMALLINT unsigned DEFAULT NULL,
 `battle_pokemon_costume`    SMALLINT unsigned DEFAULT NULL,
 `battle_pokemon_gender`     TINYINT unsigned DEFAULT NULL,
 `battle_pokemon_alignment`  SMALLINT unsigned DEFAULT NULL,
 `battle_pokemon_bread_mode` SMALLINT unsigned DEFAULT NULL,
 `battle_pokemon_move_1`     SMALLINT unsigned DEFAULT NULL,
 `battle_pokemon_move_2`     SMALLINT unsigned DEFAULT NULL,
 `battle_pokemon_stamina`    INT unsigned DEFAULT NULL,
 `battle_pokemon_cp_multiplier` FLOAT DEFAULT NULL,
 `updated`                   INT UNSIGNED NOT NULL,
 PRIMARY KEY(`bread_battle_seed`),
 KEY `ix_station_battle_station_end` (`station_id`, `battle_end`),
 KEY `ix_station_battle_end` (`battle_end`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_general_ci;
