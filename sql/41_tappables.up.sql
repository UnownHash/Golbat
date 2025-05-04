CREATE TABLE `tappable` (
  `id`              varchar(25) NOT NULL,
  `lat`             double(18,14) NOT NULL,
  `lon`             double(18,14) NOT NULL,
  `fort_id`         varchar(35) DEFAULT NULL,
  `spawnpoint_id`   varchar(35) DEFAULT NULL,
  `type`            varchar(50) NOT NULL,
  `pokemon_id`      smallint unsigned DEFAULT NULL,
  `item_id`         smallint unsigned DEFAULT NULL,
  `count`           smallint unsigned DEFAULT NULL,
  `updated`         INT UNSIGNED NOT NULL,
  PRIMARY KEY (`id`),
  KEY `ix_coords` (`lat`,`lon`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_general_ci;

