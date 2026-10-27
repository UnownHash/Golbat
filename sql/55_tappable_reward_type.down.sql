ALTER TABLE tappable
    DROP COLUMN `reward_type`,
    DROP COLUMN `reward_pokemon_id`,
    DROP COLUMN `rewards`,
    MODIFY `count` smallint unsigned DEFAULT NULL;
