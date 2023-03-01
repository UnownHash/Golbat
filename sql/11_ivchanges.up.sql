alter table pokemon
    drop index `ix_expire_timestamp_verified`,
    drop index `ix_atk_iv`,
    drop index `ix_def_iv`,
    drop index `ix_level`,
    drop index `ix_sta_iv`,
    drop index `fk_spawn_id`,
    drop index `ix_changed`,
    drop index `ix_updated`,
    drop index `fk_pokestop_id`,
    drop index `ix_iv`,
    drop index `fk_pokemon_cell_id`;

alter table pokemon
    drop primary key,
    add primary key(id);

alter table pokemon
    drop column iv;

alter table pokemon
    add column `iv` float(5,2) unsigned DEFAULT NULL;

update pokemon set iv = ((`atk_iv` + `def_iv` + `sta_iv`) * 100 / 45);

alter table pokemon
    add index `ix_iv` (`iv`),
    add index `ix_expire_timestamp_verified` (`expire_timestamp_verified`, `expire_timestamp`);
