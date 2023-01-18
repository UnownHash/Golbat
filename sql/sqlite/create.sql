CREATE TABLE pokemon (
                         id varchar(25) NOT NULL,
                         pokestop_id varchar(35),
                         spawn_id bigint,
                         lat float NOT NULL,
                         lon float NOT NULL,
                         weight float ,
                         height float ,
                         size  smallint ,
                         expire_timestamp int ,
                         updated int  ,
                         pokemon_id smallint  NOT NULL,
                         move_1 smallint  ,
                         move_2 smallint  ,
                         gender smallint  ,
                         cp smallint  ,
                         atk_iv smallint  ,
                         def_iv smallint  ,
                         sta_iv smallint  ,
                         form smallint  ,
                         level smallint  ,
                         weather smallint  ,
                         costume smallint  ,
                         first_seen_timestamp int  NOT NULL,
                         changed int   DEFAULT 0 NOT NULL,
                         `iv` float GENERATED ALWAYS AS (((((`atk_iv` + `def_iv`) + `sta_iv`) * 100) / 45)),

                         cell_id bigint  ,
                         expire_timestamp_verified TINYINT NOT NULL,
                         display_pokemon_id smallint  ,
                         seen_type varchar(20) ,
                         shiny TINYINT DEFAULT 0,
                         username varchar(32) ,
                         capture_1 float ,
                         capture_2 float ,
                         capture_3 float ,
                         pvp varchar(10),
                         is_event TINYINT NOT NULL,
                         PRIMARY KEY (id)
) ;

create index IX_encounter_id on pokemon (id);
create index `ix_coords` on pokemon (`lat`,`lon`);
create index `ix_expire_timestamp` on pokemon (`expire_timestamp`);
