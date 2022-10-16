module sylr.dev/rafty/examples/raftymcraftface

go 1.19

require (
	github.com/hashicorp/consul/api v1.15.2
	github.com/hashicorp/go-hclog v1.3.1
	github.com/hashicorp/raft v1.3.11
	github.com/nats-io/jsm.go v0.0.34
	github.com/nats-io/nats.go v1.18.0
	github.com/rs/zerolog v1.28.0
	github.com/spf13/cobra v1.6.0
	sylr.dev/rafty v0.0.0
	sylr.dev/rafty/discovery/consul v0.0.0-00010101000000-000000000000
	sylr.dev/rafty/discovery/nats v0.0.0
	sylr.dev/rafty/logger/zerolog v0.0.0
)

require (
	github.com/armon/go-metrics v0.4.1 // indirect
	github.com/buraksezer/consistent v0.9.0 // indirect
	github.com/cespare/xxhash v1.1.0 // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-msgpack v1.1.5 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/hashicorp/serf v0.10.1 // indirect
	github.com/inconshreveable/mousetrap v1.0.1 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.16 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/nats-io/nkeys v0.3.0 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/crypto v0.0.0-20221012134737-56aed061732a // indirect
	golang.org/x/sys v0.0.0-20221013171732-95e765b1cc43 // indirect
)

replace (
	sylr.dev/rafty => ../..
	sylr.dev/rafty/discovery/consul => ../../discovery/consul
	sylr.dev/rafty/discovery/nats => ../../discovery/nats
	sylr.dev/rafty/logger/zerolog => ../../logger/zerolog
)
