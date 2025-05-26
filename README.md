# Golbat

Golbat is an experimental raw data processor for Pokemon Go.
Initially designed to be database compatible with RDM, it will
be able to evolve faster by not needing to retain backward
compatibility.

# Support and discussion

There is a [Discord server](https://discord.gg/Vjze47qchG) for support and discussion.
At this time this is likely to be mostly development discussion.

# Requirements

[go 1.24](https://go.dev/doc/install)

# Instructions

1. copy `config.toml.example` to `config.toml`
2. `go run .`

## Run in pm2

1. `make` 
2. `pm2 start ./golbat --name golbat -o "/dev/null"`

## Run in docker

0. Authenticate to [GitHub Packages's docker container registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
1. copy `docker-compose.yml.example` to `docker-compose.yml`
2. modify it as you want
3. `docker compose up -d`

# Configuration of data source

The data source should be configured to send to Golbat's 
URL which will be `http://ip:port/raw`

# Scan Rules

Scan rules can be added to the configuration. These will be processed in order, first match applies - and allows disabling of processing certain types of game objects.

The scan rules can match the object to a geofence, or use the scanner 'mode' when it is supported by raw senders (looking at you Flygon!)

```toml
[[scan_rules]]
areas = ["MainArea"]
nearby_pokemon = false

[[scan_rules]]
context = ["Scout"]

[[scan_rules]]
pokemon = false
```

Here the main area would not process nearby pokemon. Messages arriving in 'scout' mode would have everything processed; and the default would not process any pokemon (so outside main area not delivered by the scout service)

pokemon - any pokemon processing (disables spawnpoints also)  
wild_pokemon - process wild pokemon from GMO  
nearby_pokemon - process nearby pokemon from GMO  
weather - process weather in GMO  
gyms - process gyms in GMO  
pokestops - process pokestops in GMO  
cells - process cell updates (disabling this also disables automatic fort clearance)

# PvP
Extra configurations for PvP are available in the `pvp` section of the config file.

```toml
# The level caps used in rankings, defaults to [50, 51]
level_caps = [50, 51]

# Gohbem uses tied rankings by default (1,1,3,4). Use the following config option to eliminate tied rankings (1,2,3,4,5) and align to external PvP Calculators like GO Stadium and PvP IVs
ranking_comparator = "prefer_higher_cp"
```

# Game objects supported support

The file [protos.md](protos.md) contains a list of the protos that are decoded by Golbat

# Optimising maria db

These options can help you quite significantly with performance.

```toml
# This should be 50% of RAM, leaving space for golbat
innodb_buffer_pool_size = 64G

# Log file size, should certainly be >= 1GB, but on a big system this is more appropriate
innodb_log_file_size = 16G

# This should be number of cores
innodb_read_io_threads = 10
innodb_write_io_threads = 10
innodb_purge_threads = 10

# Some people receommend at least 1 per gb, so could be increased above
innodb_buffer_pool_instances = 8

# allow big sorts, in memory temp tables
max_heap_table_size=256M

# extend wait timeout for locks to ensure a good chance to finish requests
innodb_lock_wait_timeout = 15

# logs are written once per second rather than after
innodb_flush_log_at_trx_commit = 0

# background tasks can work at high iops
innodb_io_capacity=1000

# Number of maximum available IOPS to background tasks
innodb_io_capacity_max=2000

# Trust disk system at the expense of recovery
innodb_doublewrite = 0
```

These last two options prevent your ibdata1 file from growing continuously
and space not being re-used.  If you have this problem you can add these but
be aware this requires recreation of ibdata.

```toml
# Efficiently use ibdata
innodb_undo_log_truncate = 1
innodb_undo_tablespaces = 4
```
