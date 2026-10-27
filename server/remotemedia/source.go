package remotemedia

// Source is a read-only, random-access image. ReadBlock has one consumer and
// may lend its buffer until the next call, as required by Serve. Close releases
// connections; cancellation of the opening context must interrupt pending I/O.
type Source interface {
	ReadBlock(offset uint64, length uint32) ([]byte, error)
	Close()
}
