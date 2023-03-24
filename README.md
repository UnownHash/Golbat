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

# Optimising maria db

innodb_buffer_pool_size