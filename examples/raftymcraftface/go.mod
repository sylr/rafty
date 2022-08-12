module sylr.dev/rafty/examples/raftymcraftface

go 1.19

require (
	github.com/hashicorp/raft v1.3.10
	github.com/nats-io/nats.go v1.16.0
	github.com/rs/zerolog v1.27.0
	github.com/spf13/cobra v1.5.0
	sylr.dev/rafty v0.0.0
	sylr.dev/rafty/discovery/nats v0.0.0
	sylr.dev/rafty/logger/zerolog v0.0.0
)

require (
	github.com/armon/go-metrics v0.4.0 // indirect
	github.com/buraksezer/consistent v0.9.0 // indirect
	github.com/cespare/xxhash v1.1.0 // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/hashicorp/go-hclog v1.2.2 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-msgpack v1.1.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/inconshreveable/mousetrap v1.0.1 // indirect
	github.com/klauspost/compress v1.14.4 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.16 // indirect
	github.com/minio/highwayhash v1.0.2 // indirect
	github.com/nats-io/jwt/v2 v2.2.1-0.20220330180145-442af02fd36a // indirect
	github.com/nats-io/nkeys v0.3.0 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/crypto v0.0.0-20220722155217-630584e8d5aa // indirect
	golang.org/x/sys v0.0.0-20220811171246-fbc7d0a398ab // indirect
	golang.org/x/time v0.0.0-20211116232009-f0f3c7e86c11 // indirect
)

replace (
	sylr.dev/rafty => ../..
	sylr.dev/rafty/discovery/nats => ../../discovery/nats
	sylr.dev/rafty/logger/zerolog => ../../logger/zerolog
)
