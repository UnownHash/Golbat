alter table gym
    ADD INDEX `ix_old_forts` (`cell_id`, `deleted`, `updated`);

alter table pokestop
    ADD INDEX `ix_old_forts` (`cell_id`, `deleted`, `updated`);
