ALTER TABLE pokemon
    DROP COLUMN `iv_inactive`,
    DROP COLUMN `encounter_weather`,
    ADD COLUMN `golbat_internal` TINYBLOB DEFAULT NULL AFTER `sta_iv`,
    ADD COLUMN `strong` BOOLEAN DEFAULT NULL AFTER `level`;
