module sylr.dev/rafty/distributor/consistent

go 1.25.0

require (
	github.com/buraksezer/consistent v0.9.0
	github.com/cespare/xxhash v1.1.0
	sylr.dev/rafty v0.0.0-20230125111348-4424dd2cf508
)

require (
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	go.etcd.io/etcd/raft/v3 v3.5.29 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace sylr.dev/rafty => ../..
