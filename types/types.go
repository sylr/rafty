package rafttypes

import "sylr.dev/rafty"

type ServerID = rafty.ServerID

type ServerAddress = rafty.ServerAddress

type ServerSuffrage = rafty.ServerSuffrage

const (
	Voter    = rafty.Voter
	Nonvoter = rafty.Nonvoter
)

type Server = rafty.Server
