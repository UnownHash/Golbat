alter table spawnpoint add column first_seen int not null default (UNIX_TIMESTAMP());
