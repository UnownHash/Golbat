alter table pokemon
    drop primary key,
    add primary key(id);

alter table pokemon
    drop column iv;

alter table pokemon
    add column `iv` float(5,2) unsigned DEFAULT NULL;

alter table pokemon
    drop index `ix_expire_timestamp_verified`;

alter table pokemon
    add index `ix_expire_timestamp_verified` (`expire_timestamp_verified`, `expire_timestamp`);
