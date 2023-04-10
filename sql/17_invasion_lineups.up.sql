ALTER TABLE `incident`
  ADD COLUMN `confirmed` tinyint unsigned NOT NULL DEFAULT 0,
  ADD COLUMN `slot_1_pokemon_id` smallint unsigned,
  ADD COLUMN  `slot_1_form` smallint unsigned,
  ADD COLUMN `slot_2_pokemon_id` smallint unsigned,
  ADD COLUMN  `slot_2_form` smallint unsigned,
  ADD COLUMN `slot_3_pokemon_id` smallint unsigned,
  ADD COLUMN  `slot_3_form` smallint unsigned;
