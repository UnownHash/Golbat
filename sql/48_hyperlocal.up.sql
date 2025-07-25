CREATE TABLE `hyperlocal` (
 `experiment_id`       INT NOT NULL,
 `start_ms`            BIGINT NOT NULL,
 `end_ms`              BIGINT NOT NULL,
 `lat`                 DOUBLE(18,14) NOT NULL,
 `lon`                 DOUBLE(18,14) NOT NULL,
 `radius_m`            DOUBLE(18,14) NOT NULL,
 `challenge_bonus_key` VARCHAR(255) NOT NULL,
 `updated_ms`          BIGINT NOT NULL,
 PRIMARY KEY(`experiment_id`,`lat`,`lon`),
 KEY `ix_end_ms` (`end_ms`,`lat`,`lon`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_general_ci;
