ALTER TABLE gym
  MODIFY COLUMN cell_id bigint default NULL;

ALTER TABLE pokemon
    MODIFY COLUMN cell_id bigint default NULL;

ALTER TABLE pokestop
    MODIFY COLUMN cell_id bigint default NULL;

alter table pokemon
    drop index `ix_expire_timestamp`;

alter table pokemon
    add index `ix_expire_timestamp_verified` (`expire_timestamp`, `expire_timestamp_verified`);

ALTER TABLE pokemon_history
    ADD KEY `expire_timestamp` (`expire_timestamp`);

DROP PROCEDURE IF EXISTS createStatsAndArchive;

create procedure createStatsAndArchive()
begin
    drop temporary table if exists old;
    create temporary table old engine = memory
    as (select id from pokemon where expire_timestamp < UNIX_TIMESTAMP() and expire_timestamp_verified = 1 UNION ALL select id from pokemon where expire_timestamp < (UNIX_TIMESTAMP()-2400) and expire_timestamp_verified = 0);

    insert into pokemon_history (id, location, pokemon_id, cp, atk_iv, def_iv, sta_iv, form, level, weather,
                                 costume, cell_id, expire_timestamp, expire_timestamp_verified, display_pokemon_id,
                                 seen_type, shiny, seen_wild, seen_stop, seen_cell, seen_lure,
                                 first_encounter, stats_reset, last_encounter, lure_encounter)
    select pokemon.id, POINT(lat,lon) as location, pokemon_id, cp, atk_iv, def_iv, sta_iv, form, level, weather,
           costume, cell_id, expire_timestamp, expire_timestamp_verified, display_pokemon_id,
           seen_type, shiny, seen_wild, seen_stop, seen_cell, seen_lure,
           first_encounter, stats_reset, last_encounter, lure_encounter
    from pokemon
             join old on old.id = pokemon.id
             left join pokemon_timing on pokemon.id = pokemon_timing.id
    on duplicate key update location=POINT(pokemon.lat,pokemon.lon), pokemon_id=pokemon.pokemon_id, cp=pokemon.cp, atk_iv=pokemon.atk_iv, def_iv=pokemon.def_iv,
                            sta_iv=pokemon.sta_iv, form=pokemon.form, level=pokemon.level, weather=pokemon.weather, costume=pokemon.costume, cell_id=pokemon.cell_id,
                            expire_timestamp=pokemon.expire_timestamp, expire_timestamp_verified=pokemon.expire_timestamp_verified,
                            display_pokemon_id= pokemon.display_pokemon_id, seen_type= pokemon.seen_type, shiny=pokemon.shiny, seen_wild=pokemon_timing.seen_wild,
                            seen_stop=pokemon_timing.seen_stop, seen_cell=pokemon_timing.seen_cell, seen_lure=pokemon_timing.seen_lure, first_encounter=pokemon_timing.first_encounter,
                            stats_reset=pokemon_timing.stats_reset, last_encounter=pokemon_timing.last_encounter, lure_encounter=pokemon_timing.lure_encounter;

    delete pokemon from pokemon
                            join old on pokemon.id = old.id;

    delete pokemon_timing from pokemon_timing
                                   join old on pokemon_timing.id = old.id;

    drop temporary table old;
end;

