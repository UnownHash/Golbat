ALTER TABLE `quest_stats`
    ADD `item_amount` SMALLINT UNSIGNED NOT NULL AFTER `item_id`,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (`date`, `area`, `fence`, `reward_type`, `pokemon_id`, `item_id`, `item_amount`) USING BTREE;
