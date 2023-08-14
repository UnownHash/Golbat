alter table `player`
    add `dex_gen9`                      TINYINT UNSIGNED DEFAULT NULL AFTER `dex_gen8a`,
    add `showcase_max_size_first_place` INT(6) UNSIGNED  DEFAULT NULL AFTER `vivillon`;

