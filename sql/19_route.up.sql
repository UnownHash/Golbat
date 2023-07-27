CREATE TABLE `route`
(
    `id`                 varchar(35)         NOT NULL,
    `name`               varchar(100)        NOT NULL,
    `description`        varchar(200)        NOT NULL,
    `distance_meters`    int unsigned        NOT NULL,
    `duration_seconds`   int unsigned        NOT NULL,
    `start_fort_id`      varchar(35)         NOT NULL,
    `start_lat`          double(18, 14)      NOT NULL,
    `start_lon`          double(18, 14)      NOT NULL,
    `end_fort_id`        varchar(35)         NOT NULL,
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
    PRIMARY KEY (`id`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;
