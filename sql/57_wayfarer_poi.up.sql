CREATE TABLE `wayfarer_poi`
(
    `id`                   varchar(64)         NOT NULL,
    `lat`                  double(18, 14)      NOT NULL,
    `lon`                  double(18, 14)      NOT NULL,
    `name`                 varchar(128)        DEFAULT NULL,
    `description`          text                DEFAULT NULL,
    `url`                  varchar(200)        DEFAULT NULL,
    `in_game`              tinyint(1) unsigned NOT NULL,
    `first_seen_timestamp` int unsigned        NOT NULL,
    `updated`              int unsigned        NOT NULL,
    PRIMARY KEY (`id`),
    KEY `ix_coords` (`lat`, `lon`),
    KEY `ix_updated` (`updated`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_general_ci;
