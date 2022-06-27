
--
-- Table structure for table `gym`
--

CREATE TABLE `gym` (
  `id` varchar(35) NOT NULL,
  `lat` double(18,14) NOT NULL,
  `lon` double(18,14) NOT NULL,
  `name` varchar(128) DEFAULT NULL,
  `url` varchar(200) DEFAULT NULL,
  `last_modified_timestamp` int unsigned DEFAULT NULL,
  `raid_end_timestamp` int unsigned DEFAULT NULL,
  `raid_spawn_timestamp` int unsigned DEFAULT NULL,
  `raid_battle_timestamp` int unsigned DEFAULT NULL,
  `updated` int unsigned NOT NULL,
  `raid_pokemon_id` smallint unsigned DEFAULT NULL,
  `guarding_pokemon_id` smallint unsigned DEFAULT NULL,
  `available_slots` smallint unsigned DEFAULT NULL,
  `availble_slots` smallint unsigned GENERATED ALWAYS AS (`available_slots`) VIRTUAL,
  `team_id` tinyint unsigned DEFAULT NULL,
  `raid_level` tinyint unsigned DEFAULT NULL,
  `enabled` tinyint unsigned DEFAULT NULL,
  `ex_raid_eligible` tinyint unsigned DEFAULT NULL,
  `in_battle` tinyint unsigned DEFAULT NULL,
  `raid_pokemon_move_1` smallint unsigned DEFAULT NULL,
  `raid_pokemon_move_2` smallint unsigned DEFAULT NULL,
  `raid_pokemon_form` smallint unsigned DEFAULT NULL,
  `raid_pokemon_cp` int unsigned DEFAULT NULL,
  `raid_is_exclusive` tinyint unsigned DEFAULT NULL,
  `cell_id` bigint unsigned DEFAULT NULL,
  `deleted` tinyint unsigned NOT NULL DEFAULT '0',
  `total_cp` int unsigned DEFAULT NULL,
  `first_seen_timestamp` int unsigned NOT NULL,
  `raid_pokemon_gender` tinyint unsigned DEFAULT NULL,
  `sponsor_id` smallint unsigned DEFAULT NULL,
  `partner_id` varchar(35) DEFAULT NULL,
  `raid_pokemon_costume` smallint unsigned DEFAULT NULL,
  `raid_pokemon_evolution` tinyint unsigned DEFAULT NULL,
  `ar_scan_eligible` tinyint unsigned DEFAULT NULL,
  `power_up_level` smallint unsigned DEFAULT NULL,
  `power_up_points` int unsigned DEFAULT NULL,
  `power_up_end_timestamp` int unsigned DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `ix_coords` (`lat`,`lon`),
  KEY `ix_raid_end_timestamp` (`raid_end_timestamp`),
  KEY `ix_updated` (`updated`),
  KEY `ix_raid_pokemon_id` (`raid_pokemon_id`),
  KEY `fk_gym_cell_id` (`cell_id`),
  KEY `ix_gym_deleted` (`deleted`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `incident`
--

CREATE TABLE `incident` (
  `id` varchar(35) NOT NULL,
  `pokestop_id` varchar(35) NOT NULL,
  `start` int unsigned NOT NULL,
  `expiration` int unsigned NOT NULL,
  `display_type` smallint unsigned NOT NULL,
  `style` smallint unsigned NOT NULL,
  `character` smallint unsigned NOT NULL,
  `updated` int unsigned NOT NULL,
  PRIMARY KEY (`id`),
  KEY `ix_pokestop` (`pokestop_id`,`expiration`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `invasion_stats`
--

DROP TABLE IF EXISTS `invasion_stats`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `invasion_stats` (
  `date` date NOT NULL,
  `grunt_type` smallint unsigned NOT NULL DEFAULT '0',
  `count` int NOT NULL,
  PRIMARY KEY (`date`,`grunt_type`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `pokemon`
--

CREATE TABLE `pokemon` (
  `id` varchar(25) NOT NULL,
  `pokestop_id` varchar(35) DEFAULT NULL,
  `spawn_id` bigint unsigned DEFAULT NULL,
  `lat` double(18,14) NOT NULL,
  `lon` double(18,14) NOT NULL,
  `weight` double(18,14) DEFAULT NULL,
  `size` double(18,14) DEFAULT NULL,
  `expire_timestamp` int unsigned DEFAULT NULL,
  `updated` int unsigned DEFAULT NULL,
  `pokemon_id` smallint unsigned NOT NULL,
  `move_1` smallint unsigned DEFAULT NULL,
  `move_2` smallint unsigned DEFAULT NULL,
  `gender` tinyint unsigned DEFAULT NULL,
  `cp` smallint unsigned DEFAULT NULL,
  `atk_iv` tinyint unsigned DEFAULT NULL,
  `def_iv` tinyint unsigned DEFAULT NULL,
  `sta_iv` tinyint unsigned DEFAULT NULL,
  `form` smallint unsigned DEFAULT NULL,
  `level` tinyint unsigned DEFAULT NULL,
  `weather` tinyint unsigned DEFAULT NULL,
  `costume` tinyint unsigned DEFAULT NULL,
  `first_seen_timestamp` int unsigned NOT NULL,
  `changed` int unsigned NOT NULL DEFAULT '0',
  `iv` float(5,2) unsigned GENERATED ALWAYS AS (((((`atk_iv` + `def_iv`) + `sta_iv`) * 100) / 45)) VIRTUAL,
  `cell_id` bigint unsigned DEFAULT NULL,
  `expire_timestamp_verified` tinyint unsigned NOT NULL,
  `display_pokemon_id` smallint unsigned DEFAULT NULL,
  `seen_type` enum('wild','encounter','nearby_stop','nearby_cell') DEFAULT NULL,
  `shiny` tinyint(1) DEFAULT '0',
  `username` varchar(32) DEFAULT NULL,
  `capture_1` double(18,14) DEFAULT NULL,
  `capture_2` double(18,14) DEFAULT NULL,
  `capture_3` double(18,14) DEFAULT NULL,
  `pvp` text,
  `is_event` tinyint unsigned NOT NULL DEFAULT '0',
  PRIMARY KEY (`id`,`is_event`),
  KEY `ix_coords` (`lat`,`lon`),
  KEY `ix_pokemon_id` (`pokemon_id`),
  KEY `ix_updated` (`updated`),
  KEY `fk_spawn_id` (`spawn_id`),
  KEY `fk_pokestop_id` (`pokestop_id`),
  KEY `ix_atk_iv` (`atk_iv`),
  KEY `ix_def_iv` (`def_iv`),
  KEY `ix_sta_iv` (`sta_iv`),
  KEY `ix_changed` (`changed`),
  KEY `ix_level` (`level`),
  KEY `fk_pokemon_cell_id` (`cell_id`),
  KEY `ix_expire_timestamp` (`expire_timestamp`),
  KEY `ix_iv` (`iv`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `pokemon_hundo_stats` (
  `date` date NOT NULL,
  `pokemon_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`date`,`pokemon_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `pokemon_iv_stats`
--

CREATE TABLE `pokemon_iv_stats` (
  `date` date NOT NULL,
  `pokemon_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`date`,`pokemon_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `pokemon_shiny_stats`
--

CREATE TABLE `pokemon_shiny_stats` (
  `date` date NOT NULL,
  `pokemon_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`date`,`pokemon_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `pokemon_stats`
--

CREATE TABLE `pokemon_stats` (
  `date` date NOT NULL,
  `pokemon_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`date`,`pokemon_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `pokestop`
--

CREATE TABLE `pokestop` (
  `id` varchar(35) NOT NULL,
  `lat` double(18,14) NOT NULL,
  `lon` double(18,14) NOT NULL,
  `name` varchar(128) DEFAULT NULL,
  `url` varchar(200) DEFAULT NULL,
  `lure_expire_timestamp` int unsigned DEFAULT NULL,
  `last_modified_timestamp` int unsigned DEFAULT NULL,
  `updated` int unsigned NOT NULL,
  `enabled` tinyint unsigned DEFAULT NULL,
  `quest_type` int unsigned DEFAULT NULL,
  `quest_timestamp` int unsigned DEFAULT NULL,
  `quest_target` smallint unsigned DEFAULT NULL,
  `quest_conditions` text,
  `quest_rewards` text,
  `quest_template` varchar(100) DEFAULT NULL,
  `quest_title` varchar(100) DEFAULT NULL,
  `quest_reward_type` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].type'),_utf8mb4'$[0]')) VIRTUAL,
  `quest_item_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.item_id'),_utf8mb4'$[0]')) VIRTUAL,
  `quest_reward_amount` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.amount'),_utf8mb4'$[0]')) VIRTUAL,
  `cell_id` bigint unsigned DEFAULT NULL,
  `deleted` tinyint unsigned NOT NULL DEFAULT '0',
  `lure_id` smallint DEFAULT '0',
  `first_seen_timestamp` int unsigned NOT NULL,
  `sponsor_id` smallint unsigned DEFAULT NULL,
  `partner_id` varchar(35) DEFAULT NULL,
  `quest_pokemon_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`quest_rewards`,_utf8mb4'$[*].info.pokemon_id'),_utf8mb4'$[0]')) VIRTUAL,
  `ar_scan_eligible` tinyint unsigned DEFAULT NULL,
  `power_up_level` smallint unsigned DEFAULT NULL,
  `power_up_points` int unsigned DEFAULT NULL,
  `power_up_end_timestamp` int unsigned DEFAULT NULL,
  `alternative_quest_type` int unsigned DEFAULT NULL,
  `alternative_quest_timestamp` int unsigned DEFAULT NULL,
  `alternative_quest_target` smallint unsigned DEFAULT NULL,
  `alternative_quest_conditions` text,
  `alternative_quest_rewards` text,
  `alternative_quest_template` varchar(100) DEFAULT NULL,
  `alternative_quest_title` varchar(100) DEFAULT NULL,
  `alternative_quest_pokemon_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`alternative_quest_rewards`,_utf8mb4'$[*].info.pokemon_id'),_utf8mb4'$[0]')) VIRTUAL,
  `alternative_quest_reward_type` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`alternative_quest_rewards`,_utf8mb4'$[*].type'),_utf8mb4'$[0]')) VIRTUAL,
  `alternative_quest_item_id` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`alternative_quest_rewards`,_utf8mb4'$[*].info.item_id'),_utf8mb4'$[0]')) VIRTUAL,
  `alternative_quest_reward_amount` smallint unsigned GENERATED ALWAYS AS (json_extract(json_extract(`alternative_quest_rewards`,_utf8mb4'$[*].info.amount'),_utf8mb4'$[0]')) VIRTUAL,
  PRIMARY KEY (`id`),
  KEY `ix_coords` (`lat`,`lon`),
  KEY `ix_lure_expire_timestamp` (`lure_expire_timestamp`),
  KEY `ix_updated` (`updated`),
  KEY `fk_pokestop_cell_id` (`cell_id`),
  KEY `ix_pokestop_deleted` (`deleted`),
  KEY `ix_quest_reward_type` (`quest_reward_type`),
  KEY `ix_quest_item_id` (`quest_item_id`),
  KEY `ix_quest_pokemon_id` (`quest_pokemon_id`),
  KEY `ix_alternative_quest_alternative_quest_pokemon_id` (`alternative_quest_pokemon_id`),
  KEY `ix_alternative_quest_reward_type` (`alternative_quest_reward_type`),
  KEY `ix_alternative_quest_item_id` (`alternative_quest_item_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `quest_stats`
--

CREATE TABLE `quest_stats` (
  `date` date NOT NULL,
  `reward_type` smallint unsigned NOT NULL DEFAULT '0',
  `pokemon_id` smallint unsigned NOT NULL DEFAULT '0',
  `item_id` smallint unsigned NOT NULL DEFAULT '0',
  `count` int NOT NULL,
  PRIMARY KEY (`date`,`reward_type`,`pokemon_id`,`item_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `raid_stats`
--

CREATE TABLE `raid_stats` (
  `date` date NOT NULL,
  `pokemon_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  `level` smallint unsigned DEFAULT NULL,
  PRIMARY KEY (`date`,`pokemon_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `s2cell`
--

CREATE TABLE `s2cell` (
  `id` bigint unsigned NOT NULL,
  `level` tinyint unsigned DEFAULT NULL,
  `center_lat` double(18,14) NOT NULL DEFAULT '0.00000000000000',
  `center_lon` double(18,14) NOT NULL DEFAULT '0.00000000000000',
  `updated` int unsigned NOT NULL,
  PRIMARY KEY (`id`),
  KEY `ix_coords` (`center_lat`,`center_lon`),
  KEY `ix_updated` (`updated`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `spawnpoint`
--

CREATE TABLE `spawnpoint` (
  `id` bigint unsigned NOT NULL,
  `lat` double(18,14) NOT NULL,
  `lon` double(18,14) NOT NULL,
  `updated` int unsigned NOT NULL DEFAULT '0',
  `last_seen` int unsigned NOT NULL DEFAULT '0',
  `despawn_sec` smallint unsigned DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `ix_coords` (`lat`,`lon`),
  KEY `ix_updated` (`updated`),
  KEY `ix_last_seen` (`last_seen`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

--
-- Table structure for table `weather`
--

CREATE TABLE `weather` (
  `id` bigint NOT NULL,
  `level` tinyint unsigned DEFAULT NULL,
  `latitude` double(18,14) NOT NULL DEFAULT '0.00000000000000',
  `longitude` double(18,14) NOT NULL DEFAULT '0.00000000000000',
  `gameplay_condition` tinyint unsigned DEFAULT NULL,
  `wind_direction` mediumint DEFAULT NULL,
  `cloud_level` tinyint unsigned DEFAULT NULL,
  `rain_level` tinyint unsigned DEFAULT NULL,
  `wind_level` tinyint unsigned DEFAULT NULL,
  `snow_level` tinyint unsigned DEFAULT NULL,
  `fog_level` tinyint unsigned DEFAULT NULL,
  `special_effect_level` tinyint unsigned DEFAULT NULL,
  `severity` tinyint unsigned DEFAULT NULL,
  `warn_weather` tinyint unsigned DEFAULT NULL,
  `updated` int unsigned NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

