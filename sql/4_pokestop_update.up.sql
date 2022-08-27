ALTER TABLE `pokestop`
    ADD COLUMN quest_expiry int unsigned NULL,
    ADD COLUMN alternative_quest_expiry int unsigned NULL,
    ADD COLUMN description text;

ALTER TABLE `pokestop` ADD INDEX `ix_quest_expiry` (`quest_expiry`);
ALTER TABLE `pokestop` ADD INDEX `ix_alternative_quest_expiry` (`alternative_quest_expiry`);

/* Update quest calculated fields */
ALTER TABLE `pokestop`
    DROP COLUMN `quest_reward_type`,
    DROP COLUMN `quest_item_id` ,
    DROP COLUMN `quest_reward_amount`,
    DROP COLUMN `quest_pokemon_id`,
    DROP COLUMN `alternative_quest_pokemon_id`,
    DROP COLUMN `alternative_quest_reward_type`,
    DROP COLUMN `alternative_quest_item_id`,
    DROP COLUMN `alternative_quest_reward_amount`;

ALTER TABLE `pokestop`
    ADD COLUMN `quest_reward_type` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].type'),_utf8mb4'$[0]')) STORED,
    ADD COLUMN `quest_item_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.item_id'),_utf8mb4'$[0]')) STORED,
    ADD COLUMN `quest_reward_amount` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.amount'),_utf8mb4'$[0]')) STORED,
    ADD COLUMN `quest_pokemon_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.pokemon_id'),_utf8mb4'$[0]')) STORED,
    ADD COLUMN `alternative_quest_pokemon_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`alternative_quest_rewards`,_utf8mb4'$[*].info.pokemon_id'),_utf8mb4'$[0]')) STORED,
    ADD COLUMN `alternative_quest_reward_type` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`alternative_quest_rewards`,_utf8mb4'$[*].type'),_utf8mb4'$[0]')) STORED,
    ADD COLUMN `alternative_quest_item_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`alternative_quest_rewards`,_utf8mb4'$[*].info.item_id'),_utf8mb4'$[0]')) STORED,
    ADD COLUMN `alternative_quest_reward_amount` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`alternative_quest_rewards`,_utf8mb4'$[*].info.amount'),_utf8mb4'$[0]')) STORED;

/* Add back indexes? */
# KEY `ix_quest_reward_type` (`quest_reward_type`),
#   KEY `ix_quest_item_id` (`quest_item_id`),
#   KEY `ix_quest_pokemon_id` (`quest_pokemon_id`),
#   KEY `ix_alternative_quest_alternative_quest_pokemon_id` (`alternative_quest_pokemon_id`),
#   KEY `ix_alternative_quest_reward_type` (`alternative_quest_reward_type`),
#   KEY `ix_alternative_quest_item_id` (`alternative_quest_item_id`)


/* Description for gym */

ALTER TABLE `gym`
    ADD COLUMN description text;

/* incident expiry time index */

ALTER TABLE `incident` ADD INDEX `ix_expiration` (`expiration`);
