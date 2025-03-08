ALTER TABLE `pokemon_stats`
DROP PRIMARY KEY,
ADD PRIMARY KEY (`date`, `area`, `fence`, `pokemon_id`, `form_id`) USING BTREE;

ALTER TABLE `pokemon_iv_stats`
DROP PRIMARY KEY,
ADD PRIMARY KEY (`date`, `area`, `fence`, `pokemon_id`, `form_id`) USING BTREE;

ALTER TABLE `pokemon_hundo_stats`
DROP PRIMARY KEY,
ADD PRIMARY KEY (`date`, `area`, `fence`, `pokemon_id`, `form_id`) USING BTREE;

ALTER TABLE `pokemon_nundo_stats`
DROP PRIMARY KEY,
ADD PRIMARY KEY (`date`, `area`, `fence`, `pokemon_id`, `form_id`) USING BTREE;

ALTER TABLE `pokemon_shiny_stats`
DROP PRIMARY KEY,
ADD PRIMARY KEY (`date`, `area`, `fence`, `pokemon_id`, `form_id`) USING BTREE;
