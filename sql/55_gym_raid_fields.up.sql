ALTER TABLE gym
    ADD `raid_seed` BIGINT DEFAULT NULL AFTER `raid_battle_timestamp`,
    ADD `raid_pokemon_stamina` INT DEFAULT NULL AFTER `raid_pokemon_cp`,
    ADD `raid_pokemon_cp_multiplier` FLOAT DEFAULT NULL AFTER `raid_pokemon_stamina`;
