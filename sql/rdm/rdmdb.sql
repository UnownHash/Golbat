-- MySQL dump 10.13  Distrib 8.0.28, for macos12.2 (arm64)
--
-- Host: localhost    Database: rdmdb
-- ------------------------------------------------------
-- Server version	8.0.28

/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!40101 SET @OLD_CHARACTER_SET_RESULTS=@@CHARACTER_SET_RESULTS */;
/*!40101 SET @OLD_COLLATION_CONNECTION=@@COLLATION_CONNECTION */;
/*!50503 SET NAMES utf8mb4 */;
/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;
/*!40103 SET TIME_ZONE='+00:00' */;
/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */;
/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;
/*!40111 SET @OLD_SQL_NOTES=@@SQL_NOTES, SQL_NOTES=0 */;

--
-- Table structure for table `account`
--

DROP TABLE IF EXISTS `account`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `account` (
  `username` varchar(32) NOT NULL,
  `password` varchar(32) NOT NULL,
  `first_warning_timestamp` int unsigned DEFAULT NULL,
  `failed_timestamp` int unsigned DEFAULT NULL,
  `failed` varchar(32) DEFAULT NULL,
  `level` tinyint unsigned NOT NULL DEFAULT '0',
  `last_encounter_lat` double(18,14) DEFAULT NULL,
  `last_encounter_lon` double(18,14) DEFAULT NULL,
  `last_encounter_time` int unsigned DEFAULT NULL,
  `spins` smallint unsigned NOT NULL DEFAULT '0',
  `creation_timestamp` int unsigned DEFAULT NULL,
  `warn` tinyint unsigned DEFAULT NULL,
  `warn_expire_timestamp` int unsigned DEFAULT NULL,
  `warn_message_acknowledged` tinyint unsigned DEFAULT NULL,
  `suspended_message_acknowledged` tinyint unsigned DEFAULT NULL,
  `was_suspended` tinyint unsigned DEFAULT NULL,
  `banned` tinyint unsigned DEFAULT NULL,
  `last_used_timestamp` int unsigned DEFAULT NULL,
  `group` varchar(50) DEFAULT NULL,
  PRIMARY KEY (`username`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `assignment`
--

DROP TABLE IF EXISTS `assignment`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `assignment` (
  `device_uuid` varchar(40) DEFAULT NULL,
  `instance_name` varchar(30) NOT NULL,
  `time` mediumint unsigned NOT NULL,
  `enabled` tinyint unsigned NOT NULL DEFAULT '1',
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `device_group_name` varchar(30) DEFAULT NULL,
  `source_instance_name` varchar(30) DEFAULT NULL,
  `date` date DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `assignment_unique` (`device_uuid`,`device_group_name`,`instance_name`,`time`,`date`),
  KEY `assignment_fk_instance_name` (`instance_name`),
  KEY `assignment_fk_source_device_group_name` (`device_group_name`),
  KEY `assignment_fk_source_instance_name` (`source_instance_name`),
  CONSTRAINT `assignment_fk_device_uuid` FOREIGN KEY (`device_uuid`) REFERENCES `device` (`uuid`) ON DELETE CASCADE ON UPDATE CASCADE,
  CONSTRAINT `assignment_fk_instance_name` FOREIGN KEY (`instance_name`) REFERENCES `instance` (`name`) ON DELETE CASCADE ON UPDATE CASCADE,
  CONSTRAINT `assignment_fk_source_device_group_name` FOREIGN KEY (`device_group_name`) REFERENCES `device_group` (`name`) ON DELETE CASCADE ON UPDATE CASCADE,
  CONSTRAINT `assignment_fk_source_instance_name` FOREIGN KEY (`source_instance_name`) REFERENCES `instance` (`name`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `assignment_group`
--

DROP TABLE IF EXISTS `assignment_group`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `assignment_group` (
  `name` varchar(30) NOT NULL,
  PRIMARY KEY (`name`),
  UNIQUE KEY `name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `assignment_group_assignment`
--

DROP TABLE IF EXISTS `assignment_group_assignment`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `assignment_group_assignment` (
  `assignment_group_name` varchar(30) NOT NULL,
  `assignment_id` int unsigned NOT NULL,
  PRIMARY KEY (`assignment_group_name`,`assignment_id`),
  KEY `assignment_group_assignment_fk_assignment_id` (`assignment_id`),
  CONSTRAINT `assignment_group_assignment_fk_assignment_group_name` FOREIGN KEY (`assignment_group_name`) REFERENCES `assignment_group` (`name`) ON DELETE CASCADE ON UPDATE CASCADE,
  CONSTRAINT `assignment_group_assignment_fk_assignment_id` FOREIGN KEY (`assignment_id`) REFERENCES `assignment` (`id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `device`
--

DROP TABLE IF EXISTS `device`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `device` (
  `uuid` varchar(40) NOT NULL,
  `instance_name` varchar(30) DEFAULT NULL,
  `last_host` varchar(34) DEFAULT NULL,
  `last_seen` int unsigned NOT NULL DEFAULT '0',
  `account_username` varchar(32) DEFAULT NULL,
  `last_lat` double DEFAULT '0',
  `last_lon` double DEFAULT '0',
  PRIMARY KEY (`uuid`),
  UNIQUE KEY `uk_iaccount_username` (`account_username`),
  KEY `fk_instance_name` (`instance_name`),
  CONSTRAINT `fk_account_username` FOREIGN KEY (`account_username`) REFERENCES `account` (`username`) ON DELETE SET NULL ON UPDATE CASCADE,
  CONSTRAINT `fk_instance_name` FOREIGN KEY (`instance_name`) REFERENCES `instance` (`name`) ON DELETE SET NULL ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `device_group`
--

DROP TABLE IF EXISTS `device_group`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `device_group` (
  `name` varchar(30) NOT NULL,
  PRIMARY KEY (`name`),
  UNIQUE KEY `name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `device_group_device`
--

DROP TABLE IF EXISTS `device_group_device`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `device_group_device` (
  `device_group_name` varchar(30) NOT NULL,
  `device_uuid` varchar(40) NOT NULL,
  PRIMARY KEY (`device_group_name`,`device_uuid`),
  KEY `device_group_device_fk_device_uuid` (`device_uuid`),
  CONSTRAINT `device_group_device_fk_device_group_name` FOREIGN KEY (`device_group_name`) REFERENCES `device_group` (`name`) ON DELETE CASCADE ON UPDATE CASCADE,
  CONSTRAINT `device_group_device_fk_device_uuid` FOREIGN KEY (`device_uuid`) REFERENCES `device` (`uuid`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `discord_rule`
--

DROP TABLE IF EXISTS `discord_rule`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `discord_rule` (
  `priority` int NOT NULL,
  `server_id` bigint unsigned NOT NULL,
  `role_id` bigint unsigned DEFAULT NULL,
  `group_name` varchar(32) NOT NULL,
  PRIMARY KEY (`priority`),
  KEY `group_name` (`group_name`),
  CONSTRAINT `discord_rule_ibfk_1` FOREIGN KEY (`group_name`) REFERENCES `group` (`name`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `group`
--

DROP TABLE IF EXISTS `group`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `group` (
  `name` varchar(32) NOT NULL,
  `perm_view_map` tinyint unsigned NOT NULL,
  `perm_view_map_raid` tinyint unsigned NOT NULL,
  `perm_view_map_pokemon` tinyint unsigned NOT NULL,
  `perm_view_stats` tinyint unsigned NOT NULL,
  `perm_admin` tinyint unsigned NOT NULL,
  `perm_view_map_gym` tinyint unsigned NOT NULL,
  `perm_view_map_pokestop` tinyint unsigned NOT NULL,
  `perm_view_map_spawnpoint` tinyint unsigned NOT NULL,
  `perm_view_map_quest` tinyint unsigned NOT NULL,
  `perm_view_map_iv` tinyint unsigned NOT NULL,
  `perm_view_map_cell` tinyint unsigned NOT NULL,
  `perm_view_map_lure` tinyint unsigned NOT NULL,
  `perm_view_map_invasion` tinyint unsigned NOT NULL,
  `perm_view_map_device` tinyint unsigned NOT NULL,
  `perm_view_map_weather` tinyint unsigned NOT NULL,
  `perm_view_map_submission_cell` tinyint unsigned NOT NULL,
  `perm_view_map_event_pokemon` tinyint unsigned NOT NULL,
  PRIMARY KEY (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET @saved_cs_results     = @@character_set_results */ ;
/*!50003 SET @saved_col_connection = @@collation_connection */ ;
/*!50003 SET character_set_client  = utf8mb4 */ ;
/*!50003 SET character_set_results = utf8mb4 */ ;
/*!50003 SET collation_connection  = utf8mb4_0900_ai_ci */ ;
/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;
/*!50003 SET sql_mode              = 'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `group_deleted` BEFORE DELETE ON `group` FOR EACH ROW UPDATE `user` SET `group_name` = "default" WHERE `group_name` = OLD.`name` */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
/*!50003 SET character_set_results = @saved_cs_results */ ;
/*!50003 SET collation_connection  = @saved_col_connection */ ;

--
-- Table structure for table `gym`
--

DROP TABLE IF EXISTS `gym`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
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
  KEY `ix_gym_deleted` (`deleted`),
  CONSTRAINT `fk_gym_cell_id` FOREIGN KEY (`cell_id`) REFERENCES `s2cell` (`id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET @saved_cs_results     = @@character_set_results */ ;
/*!50003 SET @saved_col_connection = @@collation_connection */ ;
/*!50003 SET character_set_client  = utf8mb4 */ ;
/*!50003 SET character_set_results = utf8mb4 */ ;
/*!50003 SET collation_connection  = utf8mb4_0900_ai_ci */ ;
/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;
/*!50003 SET sql_mode              = 'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `gym_inserted` AFTER INSERT ON `gym` FOR EACH ROW BEGIN
  IF (NEW.raid_pokemon_id IS NOT NULL AND NEW.raid_pokemon_id != 0) THEN
    INSERT INTO raid_stats (pokemon_id, level, count, date)
    VALUES
      (NEW.raid_pokemon_id, NEW.raid_level, 1, DATE(FROM_UNIXTIME(NEW.raid_end_timestamp)))
    ON DUPLICATE KEY UPDATE
      count = count + 1;
  END IF;
END */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
/*!50003 SET character_set_results = @saved_cs_results */ ;
/*!50003 SET collation_connection  = @saved_col_connection */ ;
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET @saved_cs_results     = @@character_set_results */ ;
/*!50003 SET @saved_col_connection = @@collation_connection */ ;
/*!50003 SET character_set_client  = utf8mb4 */ ;
/*!50003 SET character_set_results = utf8mb4 */ ;
/*!50003 SET collation_connection  = utf8mb4_0900_ai_ci */ ;
/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;
/*!50003 SET sql_mode              = 'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `gym_updated` BEFORE UPDATE ON `gym` FOR EACH ROW BEGIN
  IF ((OLD.raid_pokemon_id IS NULL OR OLD.raid_pokemon_id = 0) AND (NEW.raid_pokemon_id IS NOT NULL AND NEW.raid_pokemon_id != 0)) THEN
    INSERT INTO raid_stats (pokemon_id, level, count, date)
    VALUES
      (NEW.raid_pokemon_id, NEW.raid_level, 1, DATE(FROM_UNIXTIME(NEW.raid_end_timestamp)))
    ON DUPLICATE KEY UPDATE
      count = count + 1;
  END IF;
END */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
/*!50003 SET character_set_results = @saved_cs_results */ ;
/*!50003 SET collation_connection  = @saved_col_connection */ ;

--
-- Table structure for table `incident`
--

DROP TABLE IF EXISTS `incident`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
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
  KEY `ix_pokestop` (`pokestop_id`,`expiration`),
  CONSTRAINT `fk_incident_pokestop_id` FOREIGN KEY (`pokestop_id`) REFERENCES `pokestop` (`id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET @saved_cs_results     = @@character_set_results */ ;
/*!50003 SET @saved_col_connection = @@collation_connection */ ;
/*!50003 SET character_set_client  = utf8mb4 */ ;
/*!50003 SET character_set_results = utf8mb4 */ ;
/*!50003 SET collation_connection  = utf8mb4_0900_ai_ci */ ;
/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;
/*!50003 SET sql_mode              = 'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `invasion_inserted` AFTER INSERT ON `incident` FOR EACH ROW BEGIN
    INSERT INTO invasion_stats (grunt_type, count, date)
    VALUES (NEW.character, 1, DATE(FROM_UNIXTIME(NEW.expiration)))
    ON DUPLICATE KEY UPDATE count = count + 1;
END */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
/*!50003 SET character_set_results = @saved_cs_results */ ;
/*!50003 SET collation_connection  = @saved_col_connection */ ;
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET @saved_cs_results     = @@character_set_results */ ;
/*!50003 SET @saved_col_connection = @@collation_connection */ ;
/*!50003 SET character_set_client  = utf8mb4 */ ;
/*!50003 SET character_set_results = utf8mb4 */ ;
/*!50003 SET collation_connection  = utf8mb4_0900_ai_ci */ ;
/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;
/*!50003 SET sql_mode              = 'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `invasion_updated` BEFORE UPDATE ON `incident` FOR EACH ROW BEGIN
    IF (NEW.`character` != OLD.`character`) THEN
    INSERT INTO invasion_stats (grunt_type, count, date)
    VALUES (NEW.character, 1, DATE(FROM_UNIXTIME(NEW.expiration)))
    ON DUPLICATE KEY UPDATE count = count + 1;
    END IF;
END */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
/*!50003 SET character_set_results = @saved_cs_results */ ;
/*!50003 SET collation_connection  = @saved_col_connection */ ;

--
-- Table structure for table `instance`
--

DROP TABLE IF EXISTS `instance`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `instance` (
  `name` varchar(30) NOT NULL,
  `type` enum('circle_pokemon','circle_smart_pokemon','circle_raid','circle_smart_raid','auto_quest','pokemon_iv','leveling') NOT NULL,
  `data` longtext NOT NULL,
  PRIMARY KEY (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `metadata`
--

DROP TABLE IF EXISTS `metadata`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `metadata` (
  `key` varchar(200) NOT NULL,
  `value` longtext,
  PRIMARY KEY (`key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `pokemon`
--

DROP TABLE IF EXISTS `pokemon`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
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
  KEY `ix_iv` (`iv`),
  CONSTRAINT `fk_pokemon_cell_id` FOREIGN KEY (`cell_id`) REFERENCES `s2cell` (`id`) ON DELETE CASCADE ON UPDATE CASCADE,
  CONSTRAINT `fk_pokestop_id` FOREIGN KEY (`pokestop_id`) REFERENCES `pokestop` (`id`) ON DELETE SET NULL ON UPDATE CASCADE,
  CONSTRAINT `fk_spawn_id` FOREIGN KEY (`spawn_id`) REFERENCES `spawnpoint` (`id`) ON DELETE SET NULL ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET @saved_cs_results     = @@character_set_results */ ;
/*!50003 SET @saved_col_connection = @@collation_connection */ ;
/*!50003 SET character_set_client  = utf8mb4 */ ;
/*!50003 SET character_set_results = utf8mb4 */ ;
/*!50003 SET collation_connection  = utf8mb4_0900_ai_ci */ ;
/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;
/*!50003 SET sql_mode              = 'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `pokemon_inserted` BEFORE INSERT ON `pokemon` FOR EACH ROW BEGIN
    INSERT INTO pokemon_stats (pokemon_id, count, date)
    VALUES
        (NEW.pokemon_id, 1, DATE(FROM_UNIXTIME(NEW.expire_timestamp)))
    ON DUPLICATE KEY UPDATE
        count = count + 1;
    IF (NEW.iv IS NOT NULL) THEN BEGIN
        INSERT INTO pokemon_iv_stats (pokemon_id, count, date)
        VALUES
            (NEW.pokemon_id, 1, DATE(FROM_UNIXTIME(NEW.expire_timestamp)))
        ON DUPLICATE KEY UPDATE
            count = count + 1;
        END;
    END IF;
    IF (NEW.shiny = 1) THEN BEGIN
        INSERT INTO pokemon_shiny_stats (pokemon_id, count, date)
        VALUES
            (NEW.pokemon_id, 1, DATE(FROM_UNIXTIME(NEW.expire_timestamp)))
        ON DUPLICATE KEY UPDATE
            count = count + 1;
        END;
    END IF;
    IF (NEW.iv = 100) THEN BEGIN
        INSERT INTO pokemon_hundo_stats (pokemon_id, count, date)
        VALUES
            (NEW.pokemon_id, 1, DATE(FROM_UNIXTIME(NEW.expire_timestamp)))
        ON DUPLICATE KEY UPDATE
            count = count + 1;
        END;
    END IF;
END */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
/*!50003 SET character_set_results = @saved_cs_results */ ;
/*!50003 SET collation_connection  = @saved_col_connection */ ;
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET @saved_cs_results     = @@character_set_results */ ;
/*!50003 SET @saved_col_connection = @@collation_connection */ ;
/*!50003 SET character_set_client  = utf8mb4 */ ;
/*!50003 SET character_set_results = utf8mb4 */ ;
/*!50003 SET collation_connection  = utf8mb4_0900_ai_ci */ ;
/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;
/*!50003 SET sql_mode              = 'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `pokemon_updated` BEFORE UPDATE ON `pokemon` FOR EACH ROW BEGIN
    IF (NEW.iv IS NOT NULL AND OLD.iv IS NULL) THEN BEGIN
        INSERT INTO pokemon_iv_stats (pokemon_id, count, date)
        VALUES
            (NEW.pokemon_id, 1, DATE(FROM_UNIXTIME(NEW.expire_timestamp)))
        ON DUPLICATE KEY UPDATE
            count = count + 1;
        END;
    END IF;
    IF (NEW.shiny = 1 AND (OLD.shiny = 0 OR OLD.shiny IS NULL)) THEN BEGIN
        INSERT INTO pokemon_shiny_stats (pokemon_id, count, date)
        VALUES
            (NEW.pokemon_id, 1, DATE(FROM_UNIXTIME(NEW.expire_timestamp)))
        ON DUPLICATE KEY UPDATE
            count = count + 1;
        END;
    END IF;
    IF (NEW.iv = 100 AND OLD.iv IS NULL) THEN BEGIN
        INSERT INTO pokemon_hundo_stats (pokemon_id, count, date)
        VALUES
            (NEW.pokemon_id, 1, DATE(FROM_UNIXTIME(NEW.expire_timestamp)))
        ON DUPLICATE KEY UPDATE
            count = count + 1;
        END;
    END IF;
END */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
/*!50003 SET character_set_results = @saved_cs_results */ ;
/*!50003 SET collation_connection  = @saved_col_connection */ ;

--
-- Table structure for table `pokemon_hundo_stats`
--

DROP TABLE IF EXISTS `pokemon_hundo_stats`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `pokemon_hundo_stats` (
  `date` date NOT NULL,
  `pokemon_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`date`,`pokemon_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `pokemon_iv_stats`
--

DROP TABLE IF EXISTS `pokemon_iv_stats`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `pokemon_iv_stats` (
  `date` date NOT NULL,
  `pokemon_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`date`,`pokemon_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `pokemon_shiny_stats`
--

DROP TABLE IF EXISTS `pokemon_shiny_stats`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `pokemon_shiny_stats` (
  `date` date NOT NULL,
  `pokemon_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`date`,`pokemon_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `pokemon_stats`
--

DROP TABLE IF EXISTS `pokemon_stats`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `pokemon_stats` (
  `date` date NOT NULL,
  `pokemon_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`date`,`pokemon_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `pokestop`
--

DROP TABLE IF EXISTS `pokestop`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
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
  KEY `ix_alternative_quest_item_id` (`alternative_quest_item_id`),
  CONSTRAINT `fk_pokestop_cell_id` FOREIGN KEY (`cell_id`) REFERENCES `s2cell` (`id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET @saved_cs_results     = @@character_set_results */ ;
/*!50003 SET @saved_col_connection = @@collation_connection */ ;
/*!50003 SET character_set_client  = utf8mb4 */ ;
/*!50003 SET character_set_results = utf8mb4 */ ;
/*!50003 SET collation_connection  = utf8mb4_0900_ai_ci */ ;
/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;
/*!50003 SET sql_mode              = 'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `pokestop_inserted` AFTER INSERT ON `pokestop` FOR EACH ROW BEGIN
    IF (NEW.quest_type IS NOT NULL AND NEW.quest_type != 0) THEN
    INSERT INTO quest_stats (reward_type, pokemon_id, item_id, count, date)
    VALUES (NEW.quest_reward_type, IFNULL(NEW.quest_pokemon_id, 0), IFNULL(NEW.quest_item_id, 0), 1, DATE(FROM_UNIXTIME(NEW.quest_timestamp)))
    ON DUPLICATE KEY UPDATE count = count + 1;
    END IF;

    IF (NEW.alternative_quest_type IS NOT NULL AND NEW.alternative_quest_type != 0) THEN
    INSERT INTO quest_stats (reward_type, pokemon_id, item_id, count, date)
    VALUES (NEW.alternative_quest_reward_type, IFNULL(NEW.alternative_quest_pokemon_id, 0), IFNULL(NEW.alternative_quest_item_id, 0), 1, DATE(FROM_UNIXTIME(NEW.alternative_quest_timestamp)))
    ON DUPLICATE KEY UPDATE count = count + 1;
    END IF;
END */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
/*!50003 SET character_set_results = @saved_cs_results */ ;
/*!50003 SET collation_connection  = @saved_col_connection */ ;
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET @saved_cs_results     = @@character_set_results */ ;
/*!50003 SET @saved_col_connection = @@collation_connection */ ;
/*!50003 SET character_set_client  = utf8mb4 */ ;
/*!50003 SET character_set_results = utf8mb4 */ ;
/*!50003 SET collation_connection  = utf8mb4_0900_ai_ci */ ;
/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;
/*!50003 SET sql_mode              = 'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `pokestop_updated` BEFORE UPDATE ON `pokestop` FOR EACH ROW BEGIN
    IF ((OLD.quest_type IS NULL OR OLD.quest_type = 0) AND (NEW.quest_type IS NOT NULL AND NEW.quest_type != 0)) THEN
    INSERT INTO quest_stats (reward_type, pokemon_id, item_id, count, date)
    VALUES (NEW.quest_reward_type, IFNULL(NEW.quest_pokemon_id, 0), IFNULL(NEW.quest_item_id, 0), 1, DATE(FROM_UNIXTIME(NEW.quest_timestamp)))
    ON DUPLICATE KEY UPDATE count = count + 1;
    END IF;

    IF ((OLD.alternative_quest_type IS NULL OR OLD.alternative_quest_type = 0) AND NEW.alternative_quest_type IS NOT NULL AND NEW.quest_type != 0) THEN
    INSERT INTO quest_stats (reward_type, pokemon_id, item_id, count, date)
    VALUES (NEW.alternative_quest_reward_type, IFNULL(NEW.alternative_quest_pokemon_id, 0), IFNULL(NEW.alternative_quest_item_id, 0), 1, DATE(FROM_UNIXTIME(NEW.alternative_quest_timestamp)))
    ON DUPLICATE KEY UPDATE count = count + 1;
    END IF;
END */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
/*!50003 SET character_set_results = @saved_cs_results */ ;
/*!50003 SET collation_connection  = @saved_col_connection */ ;

--
-- Table structure for table `quest_stats`
--

DROP TABLE IF EXISTS `quest_stats`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `quest_stats` (
  `date` date NOT NULL,
  `reward_type` smallint unsigned NOT NULL DEFAULT '0',
  `pokemon_id` smallint unsigned NOT NULL DEFAULT '0',
  `item_id` smallint unsigned NOT NULL DEFAULT '0',
  `count` int NOT NULL,
  PRIMARY KEY (`date`,`reward_type`,`pokemon_id`,`item_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `raid_stats`
--

DROP TABLE IF EXISTS `raid_stats`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `raid_stats` (
  `date` date NOT NULL,
  `pokemon_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  `level` smallint unsigned DEFAULT NULL,
  PRIMARY KEY (`date`,`pokemon_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `s2cell`
--

DROP TABLE IF EXISTS `s2cell`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `s2cell` (
  `id` bigint unsigned NOT NULL,
  `level` tinyint unsigned DEFAULT NULL,
  `center_lat` double(18,14) NOT NULL DEFAULT '0.00000000000000',
  `center_lon` double(18,14) NOT NULL DEFAULT '0.00000000000000',
  `updated` int unsigned NOT NULL,
  PRIMARY KEY (`id`),
  KEY `ix_coords` (`center_lat`,`center_lon`),
  KEY `ix_updated` (`updated`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `spawnpoint`
--

DROP TABLE IF EXISTS `spawnpoint`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `token`
--

DROP TABLE IF EXISTS `token`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `token` (
  `token` varchar(50) NOT NULL,
  `type` enum('confirm_email','reset_password') NOT NULL,
  `username` varchar(32) NOT NULL,
  `expire_timestamp` int unsigned NOT NULL,
  PRIMARY KEY (`token`),
  KEY `fk_tokem_username` (`username`),
  KEY `ix_expire_timestamp` (`expire_timestamp`),
  CONSTRAINT `token_ibfk_1` FOREIGN KEY (`username`) REFERENCES `user` (`username`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `user`
--

DROP TABLE IF EXISTS `user`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `user` (
  `username` varchar(32) NOT NULL,
  `email` varchar(128) NOT NULL,
  `password` varchar(72) NOT NULL,
  `discord_id` bigint unsigned DEFAULT NULL,
  `email_verified` tinyint unsigned DEFAULT '0',
  `group_name` varchar(32) NOT NULL DEFAULT 'default',
  PRIMARY KEY (`username`),
  UNIQUE KEY `email` (`email`),
  KEY `fk_group_name` (`group_name`),
  KEY `ix_user_discord_id` (`discord_id`),
  CONSTRAINT `fk_group_name` FOREIGN KEY (`group_name`) REFERENCES `group` (`name`) ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `weather`
--

DROP TABLE IF EXISTS `weather`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `web_session`
--

DROP TABLE IF EXISTS `web_session`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `web_session` (
  `token` varchar(255) NOT NULL,
  `userid` varchar(255) DEFAULT NULL,
  `created` int NOT NULL DEFAULT '0',
  `updated` int NOT NULL DEFAULT '0',
  `idle` int NOT NULL DEFAULT '0',
  `data` text,
  `ipaddress` varchar(255) DEFAULT NULL,
  `useragent` text,
  PRIMARY KEY (`token`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `webhook`
--

DROP TABLE IF EXISTS `webhook`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `webhook` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `name` varchar(30) NOT NULL,
  `url` varchar(256) NOT NULL,
  `delay` double DEFAULT '5',
  `types` longtext,
  `data` longtext,
  `enabled` tinyint unsigned DEFAULT '1',
  PRIMARY KEY (`id`),
  UNIQUE KEY `name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */;

/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;
/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;
/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */;
/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;
/*!40101 SET CHARACTER_SET_RESULTS=@OLD_CHARACTER_SET_RESULTS */;
/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */;
/*!40111 SET SQL_NOTES=@OLD_SQL_NOTES */;

-- Dump completed on 2022-05-29 17:34:21
