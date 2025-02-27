ALTER TABLE `pokemon_stats` ADD `form_id` SMALLINT(5) UNSIGNED NOT NULL AFTER `pokemon_id`;
ALTER TABLE `pokemon_iv_stats` ADD `form_id` SMALLINT(5) UNSIGNED NOT NULL AFTER `pokemon_id`;
ALTER TABLE `pokemon_hundo_stats` ADD `form_id` SMALLINT(5) UNSIGNED NOT NULL AFTER `pokemon_id`;
ALTER TABLE `pokemon_nundo_stats` ADD `form_id` SMALLINT(5) UNSIGNED NOT NULL AFTER `pokemon_id`;
ALTER TABLE `pokemon_shiny_stats` ADD `form_id` SMALLINT(5) UNSIGNED NOT NULL AFTER `pokemon_id`;
