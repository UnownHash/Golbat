ALTER TABLE `station`
ADD COLUMN `battle_pokemon_bread_mode` SMALLINT unsigned AFTER `battle_pokemon_alignment`,
ADD COLUMN `battle_pokemon_move_2` SMALLINT unsigned AFTER `battle_pokemon_bread_mode`,
ADD COLUMN `battle_pokemon_move_1` SMALLINT unsigned AFTER `battle_pokemon_bread_mode`;
