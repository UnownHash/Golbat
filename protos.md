# Proto Support

The following protos are supported:

`Method_METHOD_GET_MAP_OBJECTS`

- wild pokemon (pokemon without stats, spawnpoints, spawnpoint tth)
- nearby pokemon (pokemon with very basic data)
- forts (gyms and pokestops, together with most attributes)

`Method_METHOD_GET_MAP_FORTS`

- Bulk images and names of Forts

`Method_METHOD_FORT_DETAILS`

- Get additional details of a fort. For a pokestop Golbat
will decode and store the lure end time.

`Method_METHOD_GYM_GET_INFO`

- Get details of a gym (name, team, etc).

`Method_METHOD_ENCOUNTER`

- Decode full details (IV etc) of a pokemon

`Method_METHOD_DISK_ENCOUNTER`

- Decode full details (IV etc) of a pokemon at a lure

`Method_METHOD_FORT_SEARCH`

- Decode details of quest type and rewards. This requires
the `have_ar` parameter in the raw to be present.

`Method_METHOD_INVASION_OPEN_COMBAT_SESSION`

- Provides line up of pokemon in an invasion. This
requires the proto request to be present in the raw.

`Method_METHOD_START_INCIDENT`

- Provides confirmation of a real or decoy Giovanni

`Method_METHOD_GET_ROUTES`

- Decode routes

# Social actions

- The master `ClientAction_CLIENT_ACTION_PROXY_SOCIAL_ACTION` proto will be
decoded, but requires the proto request to be decoded to determine the
exact action.

`SocialAction_SOCIAL_ACTION_LIST_FRIEND_STATUS`

- Update player record with details contained

`SocialAction_SOCIAL_ACTION_SEARCH_PLAYER`

- Update player record with details contained
