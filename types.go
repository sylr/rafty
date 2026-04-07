package rafty

type ServerID string

type ServerAddress string

type ServerSuffrage uint8

const (
	Voter ServerSuffrage = iota
	Nonvoter
)

type Server struct {
	ID       ServerID
	Address  ServerAddress
	Suffrage ServerSuffrage
}
