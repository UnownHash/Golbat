alter table `player`
    add `event_check_ins`   INT(6) UNSIGNED DEFAULT NULL AFTER `showcase_max_size_first_place`,
    add `parties_completed` INT(6) UNSIGNED DEFAULT NULL AFTER `showcase_max_size_first_place`,
    add `total_route_play`  INT(6) UNSIGNED  DEFAULT NULL AFTER `showcase_max_size_first_place`;
