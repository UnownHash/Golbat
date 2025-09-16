ALTER TABLE route
    ADD COLUMN shortcode varchar(255) NOT NULL DEFAULT '' AFTER name;
