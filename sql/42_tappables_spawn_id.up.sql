ALTER TABLE tappable
    ADD spawn_id BIGINT UNSIGNED NULL AFTER `fort_id`;

UPDATE tappable
    SET spawn_id = CONV(spawnpoint_id, 16, 10);

ALTER TABLE tappable DROP spawnpoint_id;