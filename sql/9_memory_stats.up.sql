ALTER TABLE `pokemon_hundo_stats`
    ADD COLUMN area varchar(40) NOT NULL DEFAULT '',
    ADD COLUMN fence varchar(40) NOT NULL DEFAULT '',
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (`date`, area, fence, pokemon_id) ;

ALTER TABLE `pokemon_iv_stats`
    ADD COLUMN area varchar(40) NOT NULL DEFAULT '',
    ADD COLUMN fence varchar(40) NOT NULL DEFAULT '',
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (`date`, area, fence, pokemon_id);

--
-- Table structure for table `pokemon_shiny_stats`
--

ALTER TABLE `pokemon_shiny_stats`
    ADD COLUMN area varchar(40) NOT NULL DEFAULT '',
    ADD COLUMN fence varchar(40) NOT NULL DEFAULT '',
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (`date`, area, fence, pokemon_id);

--
-- Table structure for table `pokemon_stats`
--

ALTER TABLE `pokemon_stats`
    ADD COLUMN area varchar(40) NOT NULL DEFAULT '',
    ADD COLUMN fence varchar(40) NOT NULL DEFAULT '',
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (`date`, area, fence, pokemon_id);

CREATE TABLE `pokemon_nundo_stats` (
                                       `date` date NOT NULL,
                                       `area` varchar(40) NOT NULL DEFAULT '',
                                       `fence` varchar(40) NOT NULL DEFAULT '',
                                       `pokemon_id` smallint unsigned NOT NULL,
                                       `count` int NOT NULL,
                                       PRIMARY KEY (`date`, area, fence, `pokemon_id`)
) ;

CREATE TABLE  `pokemon_area_stats` (
    `datetime` int(11) NOT NULL,
    `area` varchar(40) NOT NULL,
    `fence` varchar(40) NOT NULL,
    `totMon` int(11) DEFAULT NULL,
    `ivMon` int(11) DEFAULT NULL,
    `verifiedEnc` int(11) DEFAULT NULL,
    `unverifiedEnc` int(11) DEFAULT NULL,
    `verifiedReEnc` int(11) DEFAULT NULL,
    `encSecLeft` int(11) DEFAULT NULL,
    `encTthMax5` int(11) DEFAULT NULL,
    `encTth5to10` int(11) DEFAULT NULL,
    `encTth10to15` int(11) DEFAULT NULL,
    `encTth15to20` int(11) DEFAULT NULL,
    `encTth20to25` int(11) DEFAULT NULL,
    `encTth25to30` int(11) DEFAULT NULL,
    `encTth30to35` int(11) DEFAULT NULL,
    `encTth35to40` int(11) DEFAULT NULL,
    `encTth40to45` int(11) DEFAULT NULL,
    `encTth45to50` int(11) DEFAULT NULL,
    `encTth50to55` int(11) DEFAULT NULL,
    `encTthMin55` int(11) DEFAULT NULL,
    `resetMon` int(11) DEFAULT NULL,
    `re_encSecLeft` int(11) DEFAULT NULL,
    `numWiEnc` int(11) DEFAULT NULL,
    `secWiEnc` int(11) DEFAULT NULL,

    PRIMARY KEY (`datetime`,`area`,`fence`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
