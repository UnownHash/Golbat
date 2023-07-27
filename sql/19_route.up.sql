CREATE TABLE `route`
(
    `id`                 varchar(35)         NOT NULL,
    `name`               varchar(50)         NOT NULL,
    `description`        varchar(255)        NOT NULL,
    `distance_meters`    int unsigned        NOT NULL,
    `duration_seconds`   int unsigned        NOT NULL,
    `start_fort_id`      varchar(35)         NOT NULL,
    `start_image`        varchar(200)        NOT NULL,
    `start_lat`          double(18, 14)      NOT NULL,
    `start_lon`          double(18, 14)      NOT NULL,
    `end_fort_id`        varchar(35)         NOT NULL,
    `end_image`          varchar(200)        NOT NULL,
    `end_lat`            double(18, 14)      NOT NULL,
    `end_lon`            double(18, 14)      NOT NULL,
    `image`              varchar(200)        NOT NULL,
    `image_border_color` varchar(10)         NOT NULL,
    `reversible`         tinyint(1) unsigned NOT NULL,
    `tags`               text DEFAULT NULL,
    `type`               tinyint unsigned    NOT NULL,
    `updated`            int unsigned        NOT NULL,
    `version`            int unsigned        NOT NULL,
    `waypoints`          text                NOT NULL,
    PRIMARY KEY (`id`),
    KEY `ix_coords_start` (`start_lat`, `start_lon`),
    KEY `ix_coords_end` (`end_lat`, `end_lon`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_general_ci;
