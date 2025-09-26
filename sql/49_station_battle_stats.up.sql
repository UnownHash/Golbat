ALTER TABLE `station`
ADD COLUMN `battle_pokemon_stamina` INT unsigned AFTER `battle_pokemon_move_2`,
ADD COLUMN `battle_pokemon_cp_multiplier` FLOAT AFTER `battle_pokemon_stamina`;
