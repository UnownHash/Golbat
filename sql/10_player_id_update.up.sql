# Rename `id` to `friendship_id`
ALTER TABLE `player`
    CHANGE `id` `friendship_id` VARCHAR(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NULL DEFAULT NULL;
