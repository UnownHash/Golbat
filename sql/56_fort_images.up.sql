ALTER TABLE pokestop
    add `images` TEXT default NULL AFTER `url`;

ALTER TABLE gym
    add `images` TEXT default NULL AFTER `url`;
