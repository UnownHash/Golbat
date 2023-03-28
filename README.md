# Golbat

Golbat is an experimental raw data processor for Pokemon Go.
Initially designed to be database compatible with RDM, it will
be able to evolve faster by not needing to retain backward
compatibility.

# Support and discussion

There is a [Discord server](https://discord.gg/Vjze47qchG) for support and discussion.
At this time this is likely to be mostly development discussion.

# Requirements

`go 1.18` - although these days you should be installing go 1.20

On Ubuntu 22 (jammy), installing using apt 
`sudo apt install golang-go` should install the right version.

To get Go 1.18 on earlier versions of Ubuntu, I followed 
[this](https://nextgentips.com/2021/12/23/how-to-install-go-1-18-on-ubuntu-20-04/) 
guide. Instead of the download link given there, you can use 
`https://go.dev/dl/go1.18.3.linux-amd64.tar.gz`, 
which is the latest version as of writing this.

# Instructions

1. copy `config.toml.example` to `config.toml`
2. `go run .`

## Run in pm2

1. `go build golbat`
2. `pm2 start golbat`

## Run in docker

0. Authenticate to [GitHub Packages's docker container registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
1. copy `docker-compose.yml.example` to `docker-compose.yml`
2. modify it as you want
3. `docker-compose up -d`

# Configuration of data source

The data source should be configured to send to Golbat's 
URL which will be `http://ip:port/raw`

# Tuning

There can be a tuning section in the config file

```toml
[tuning]
# process_wild_pokemon = true
# process_nearby_pokemon = true
```

* process_wilds - by default Golbat will process the wilds from the GMO after a 15 second
delay. This allows time for your MITM to send an encounter to overtake it saving a disk write. If
you are confident that your MITM is sending encounters, you can disable wilds in all cases
saving some CPU and memory
* process_nearby - by default Golbat will process the nearby pokestop and cell pokemon from the GMO.
This comes at a cost of ~20% more disk writes than ignoring them.  Many argue these are vanity
pokemon and not worth the cost.  Some argue that early sight of a rare pokemon is worth the cost.

# Optimising maria db

These options can help you quite significantly with performance.

```
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