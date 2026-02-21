-- Convert quest reward generated columns to regular columns
-- These will now be calculated in code when receiving quest protos

-- -- Drop indexes first
-- ALTER TABLE `pokestop`
--     DROP INDEX `ix_quest_reward_type`,
--     DROP INDEX `ix_quest_item_id`,
--     DROP INDEX `ix_quest_pokemon_id`,
--     DROP INDEX `ix_alternative_quest_alternative_quest_pokemon_id`,
--     DROP INDEX `ix_alternative_quest_reward_type`,
--     DROP INDEX `ix_alternative_quest_item_id`;

-- Drop generated columns
ALTER TABLE `pokestop`
    DROP COLUMN `quest_reward_type`,
    DROP COLUMN `quest_item_id`,
    DROP COLUMN `quest_reward_amount`,
    DROP COLUMN `quest_pokemon_id`,
    DROP COLUMN `alternative_quest_reward_type`,
    DROP COLUMN `alternative_quest_item_id`,
    DROP COLUMN `alternative_quest_reward_amount`,
    DROP COLUMN `alternative_quest_pokemon_id`;

-- Re-add as regular columns
ALTER TABLE `pokestop`
    ADD COLUMN `quest_reward_type` smallint unsigned DEFAULT NULL,
    ADD COLUMN `quest_item_id` smallint unsigned DEFAULT NULL,
    ADD COLUMN `quest_reward_amount` smallint unsigned DEFAULT NULL,
    ADD COLUMN `quest_pokemon_id` smallint unsigned DEFAULT NULL,
    ADD COLUMN `quest_pokemon_form_id` smallint unsigned DEFAULT NULL,
    ADD COLUMN `alternative_quest_reward_type` smallint unsigned DEFAULT NULL,
    ADD COLUMN `alternative_quest_item_id` smallint unsigned DEFAULT NULL,
    ADD COLUMN `alternative_quest_reward_amount` smallint unsigned DEFAULT NULL,
    ADD COLUMN `alternative_quest_pokemon_id` smallint unsigned DEFAULT NULL,
    ADD COLUMN `alternative_quest_pokemon_form_id` smallint unsigned DEFAULT NULL;

-- -- Re-add indexes
-- ALTER TABLE `pokestop`
--     ADD INDEX `ix_quest_reward_type` (`quest_reward_type`),
--     ADD INDEX `ix_quest_item_id` (`quest_item_id`),
--     ADD INDEX `ix_quest_pokemon_id` (`quest_pokemon_id`),
--     ADD INDEX `ix_alternative_quest_reward_type` (`alternative_quest_reward_type`),
--     ADD INDEX `ix_alternative_quest_item_id` (`alternative_quest_item_id`),
--     ADD INDEX `ix_alternative_quest_pokemon_id` (`alternative_quest_pokemon_id`);
