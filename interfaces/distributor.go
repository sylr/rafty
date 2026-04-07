package interfaces

import "sylr.dev/rafty"

type Distributor[T any, T2 Work[T]] = rafty.Distributor[T, T2]
