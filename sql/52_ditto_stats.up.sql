-- pokemon_stats
CREATE TEMPORARY TABLE tmp_merge AS
SELECT date, area, fence, pokemon_id, 0 AS form_id, SUM(count) AS count
FROM pokemon_stats WHERE pokemon_id = 132
GROUP BY date, area, fence, pokemon_id;
DELETE FROM pokemon_stats WHERE pokemon_id = 132;
INSERT INTO pokemon_stats SELECT * FROM tmp_merge;
DROP TABLE IF EXISTS tmp_merge;

-- pokemon_iv_stats
CREATE TEMPORARY TABLE tmp_merge AS
SELECT date, area, fence, pokemon_id, 0 AS form_id, SUM(count) AS count
FROM pokemon_iv_stats WHERE pokemon_id = 132
GROUP BY date, area, fence, pokemon_id;
DELETE FROM pokemon_iv_stats WHERE pokemon_id = 132;
INSERT INTO pokemon_iv_stats SELECT * FROM tmp_merge;
DROP TABLE IF EXISTS tmp_merge;

-- pokemon_hundo_stats
CREATE TEMPORARY TABLE tmp_merge AS
SELECT date, area, fence, pokemon_id, 0 AS form_id, SUM(count) AS count
FROM pokemon_hundo_stats WHERE pokemon_id = 132
GROUP BY date, area, fence, pokemon_id;
DELETE FROM pokemon_hundo_stats WHERE pokemon_id = 132;
INSERT INTO pokemon_hundo_stats SELECT * FROM tmp_merge;
DROP TABLE IF EXISTS tmp_merge;

-- pokemon_nundo_stats
CREATE TEMPORARY TABLE tmp_merge AS
SELECT date, area, fence, pokemon_id, 0 AS form_id, SUM(count) AS count
FROM pokemon_nundo_stats WHERE pokemon_id = 132
GROUP BY date, area, fence, pokemon_id;
DELETE FROM pokemon_nundo_stats WHERE pokemon_id = 132;
INSERT INTO pokemon_nundo_stats SELECT * FROM tmp_merge;
DROP TABLE IF EXISTS tmp_merge;

-- pokemon_shiny_stats
CREATE TEMPORARY TABLE tmp_merge AS
SELECT date, area, fence, pokemon_id, 0 AS form_id, SUM(count) AS count, SUM(total) AS total
FROM pokemon_shiny_stats WHERE pokemon_id = 132
GROUP BY date, area, fence, pokemon_id;
DELETE FROM pokemon_shiny_stats WHERE pokemon_id = 132;
INSERT INTO pokemon_shiny_stats SELECT * FROM tmp_merge;
DROP TABLE IF EXISTS tmp_merge;
