# Proto Support

The following protos are supported:

`Method_METHOD_GET_MAP_OBJECTS`

Bulk update of nearby pokemon, gyms, pokestops, currently active lures,
and spawnpoints tth

`Method_METHOD_GET_MAP_FORTS`

Bulk update of forts (gyms and pokestops)

`Method_METHOD_FORT_DETAILS`

Get additional details of a gym or pokestop. For a pokestop Golbat
will decode and store the lure end time.

`Method_METHOD_GYM_GET_INFO`

Get details of a gym (name, team, etc).

`Method_METHOD_ENCOUNTER`

Decode full details (IV etc) of a pokemon

`Method_METHOD_DISK_ENCOUNTER`

Decode full details (IV etc) of a pokemon at a lure

`Method_METHOD_FORT_SEARCH`

Decode details of quest type and rewards. This requires
the `have_ar` parameter in the raw to be present.

`Method_METHOD_INVASION_OPEN_COMBAT_SESSION`

* Requires proto request

Provides line up of pokemon in an invasion

`Method_METHOD_START_INCIDENT`

Provides confirmation of a real or decoy Giovanni

# Social actions

The master `ClientAction_CLIENT_ACTION_PROXY_SOCIAL_ACTION` proto will be
decoded, but requires the proto request to be decoded to determine the
exact action.

`SocialAction_SOCIAL_ACTION_LIST_FRIEND_STATUS`

Update player record with details contained

`SocialAction_SOCIAL_ACTION_SEARCH_PLAYER`

Update player record with details contained
