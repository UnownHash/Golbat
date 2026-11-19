CREATE TABLE biome_cell (
    cell_id BIGINT UNSIGNED NOT NULL,
    geography JSON NOT NULL,
    biome_key BINARY(32) DEFAULT NULL,
    spawn_scope VARBINARY(72) NOT NULL,
    observed_at_ms BIGINT NOT NULL,
    geography_changed_at_ms BIGINT NOT NULL,
    PRIMARY KEY (cell_id)
) ENGINE=InnoDB;

CREATE TABLE biome_spawn_observation (
    encounter_key BINARY(16) NOT NULL,
    cell_id BIGINT UNSIGNED NOT NULL,
    biome_key BINARY(32) NOT NULL,
    spawn_scope VARBINARY(72) NOT NULL,
    weather INT NOT NULL,
    pokemon_id INT NOT NULL,
    form INT NOT NULL,
    observed_at_ms BIGINT NOT NULL,
    PRIMARY KEY (encounter_key),
    KEY ix_biome_observation_time (observed_at_ms)
) ENGINE=InnoDB;

CREATE TABLE biome_spawn_group (
    spawn_scope VARBINARY(72) NOT NULL,
    biome_key BINARY(32) NOT NULL,
    weather INT NOT NULL,
    sample_count BIGINT UNSIGNED NOT NULL,
    cell_count INT UNSIGNED NOT NULL,
    window_start_ms BIGINT NOT NULL,
    window_end_ms BIGINT NOT NULL,
    valid_until_ms BIGINT NOT NULL,
    PRIMARY KEY (spawn_scope, biome_key, weather)
) ENGINE=InnoDB;

CREATE TABLE biome_spawn_rate (
    spawn_scope VARBINARY(72) NOT NULL,
    biome_key BINARY(32) NOT NULL,
    weather INT NOT NULL,
    pokemon_id INT NOT NULL,
    form INT NOT NULL,
    percent DECIMAL(18,12) UNSIGNED NOT NULL,
    PRIMARY KEY (spawn_scope, biome_key, weather, pokemon_id, form),
    FOREIGN KEY (spawn_scope, biome_key, weather)
        REFERENCES biome_spawn_group (spawn_scope, biome_key, weather) ON DELETE CASCADE
) ENGINE=InnoDB;
