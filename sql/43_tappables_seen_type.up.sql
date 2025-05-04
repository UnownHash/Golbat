alter table pokemon
    modify seen_type enum ('wild', 'encounter', 'nearby_stop', 'nearby_cell', 'lure_wild', 'lure_encounter', 'tappable_encounter') null;

alter table pokemon_history
    modify seen_type enum ('wild', 'encounter', 'nearby_stop', 'nearby_cell', 'lure_wild', 'lure_encounter', 'tappable_encounter') null;
