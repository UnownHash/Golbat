-- pokemon_stats
INSERT INTO pokemon_stats (date, area, fence, pokemon_id, form_id, count)
SELECT date, area, fence, pokemon_id, 0, SUM(count)
FROM pokemon_stats WHERE pokemon_id = 132 AND form_id != 0
GROUP BY date, area, fence, pokemon_id
ON DUPLICATE KEY UPDATE count = count + VALUES(count);
DELETE FROM pokemon_stats WHERE pokemon_id = 132 AND form_id != 0;

-- pokemon_iv_stats
INSERT INTO pokemon_iv_stats (date, area, fence, pokemon_id, form_id, count)
SELECT date, area, fence, pokemon_id, 0, SUM(count)
FROM pokemon_iv_stats WHERE pokemon_id = 132 AND form_id != 0
GROUP BY date, area, fence, pokemon_id
ON DUPLICATE KEY UPDATE count = count + VALUES(count);
DELETE FROM pokemon_iv_stats WHERE pokemon_id = 132 AND form_id != 0;

-- pokemon_hundo_stats
INSERT INTO pokemon_hundo_stats (date, area, fence, pokemon_id, form_id, count)
SELECT date, area, fence, pokemon_id, 0, SUM(count)
FROM pokemon_hundo_stats WHERE pokemon_id = 132 AND form_id != 0
GROUP BY date, area, fence, pokemon_id
ON DUPLICATE KEY UPDATE count = count + VALUES(count);
DELETE FROM pokemon_hundo_stats WHERE pokemon_id = 132 AND form_id != 0;

-- pokemon_nundo_stats
INSERT INTO pokemon_nundo_stats (date, area, fence, pokemon_id, form_id, count)
SELECT date, area, fence, pokemon_id, 0, SUM(count)
FROM pokemon_nundo_stats WHERE pokemon_id = 132 AND form_id != 0
GROUP BY date, area, fence, pokemon_id
ON DUPLICATE KEY UPDATE count = count + VALUES(count);
DELETE FROM pokemon_nundo_stats WHERE pokemon_id = 132 AND form_id != 0;

-- pokemon_shiny_stats
INSERT INTO pokemon_shiny_stats (date, area, fence, pokemon_id, form_id, count, total)
SELECT date, area, fence, pokemon_id, 0, SUM(count), SUM(total)
FROM pokemon_shiny_stats WHERE pokemon_id = 132 AND form_id != 0
GROUP BY date, area, fence, pokemon_id
ON DUPLICATE KEY UPDATE count = count + VALUES(count), total = total + VALUES(total);
DELETE FROM pokemon_shiny_stats WHERE pokemon_id = 132 AND form_id != 0;
