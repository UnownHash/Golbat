ALTER TABLE tappable
    ADD `reward_type` varchar(64) DEFAULT NULL AFTER `pokemon_id`,
    ADD `reward_pokemon_id` smallint unsigned DEFAULT NULL AFTER `item_id`,
    MODIFY `count` int DEFAULT NULL,
    ADD `rewards` text DEFAULT NULL AFTER `count`;

UPDATE tappable
    SET reward_type = 'item',
        rewards = JSON_ARRAY(JSON_OBJECT('item', item_id, 'count', count))
    WHERE item_id IS NOT NULL;
