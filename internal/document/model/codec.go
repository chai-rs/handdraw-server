package model

// Codec isolates the supported content schema from the CRDT implementation.
//
//mockery:generate: true
type Codec interface {
	Encode(Snapshot, Validation) ([]byte, error)
	Decode([]byte, Validation) (Snapshot, error)
	Apply([]byte, []byte, Validation) ([]byte, error)
}
