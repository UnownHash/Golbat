ALTER TABLE tappable
    ADD `reward_type` varchar(32) DEFAULT NULL AFTER `pokemon_id`,
    ADD `reward_pokemon_id` smallint unsigned DEFAULT NULL AFTER `item_id`,
    ADD `rewards` text DEFAULT NULL AFTER `count`;

UPDATE tappable
    SET reward_type = 'item'
    WHERE item_id IS NOT NULL;
