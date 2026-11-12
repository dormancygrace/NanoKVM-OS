# NanoKVM SCTP changes

Base: `github.com/pion/sctp v1.12.1`. The upstream MIT license is retained;
only the NanoKVM test (`nanokvm_marshal_test.go`) is kept. `UPSTREAM-SHA256.json`
lists the hashes of the upstream files; the original module is available
through the Go module proxy for comparison.

The fork exists for the send path of a data channel that carries video (the
RustDesk WebRTC bridge): about 830 DATA packets for a megabyte. The protocol
and what goes on the wire are unchanged.

- **One copy of a DATA packet instead of four.** `packet.marshalInto`
  (`packet.go`) writes DATA chunks straight into the packet
  (`chunkPayloadData.appendTo`); upstream copies each through
  `chunkPayloadData.marshal`, `chunkHeader.marshal` and `packet.marshal`, and
  keeps the first copy in the chunk until it is acknowledged. The packets of
  one `gatherOutbound` are marshaled into buffers the association keeps
  (`marshalGathered`, `keptPackets` in `association.go`): the write loop has
  written them before it gathers again. Only for a connection that says its
  `Write` copies what it is given, with a method `WriteCopies` (the DTLS fork's
  connection has it): a connection that keeps the slices, as the in-memory one
  of the upstream tests does, would see them change, so for any other
  connection every packet gets its own memory as upstream.
- **At most 16 new DATA packets for one `gatherOutbound`** (`maxGatherPackets`
  in `popPendingDataChunksToSend`), the rest at once with the next one.
  Upstream takes all the congestion window allows, hundreds of packets when
  the sender is the bottleneck, and marshals them all before the first is
  written, under the association lock.
- **A burst goes to the connection in one call** when the connection has `WriteBatch` (the DTLS fork's has: it seals the records of the burst together on the crypto engine) and the burst has no ABORT, which the write loop tells apart. Any other connection gets a `Write` for every packet, as upstream.
- **One copy of a message and one array of its chunks** in `Stream.packetize`
  instead of one allocation of each for every fragment. They stay until the
  last fragment of the message is acknowledged.
- **`Tracef` calls are compiled out** (`traceLog` in `association.go`). There
  are 45 on the path of every packet sent and acknowledged, and each allocates
  for its arguments before the logger can drop the message: about ten
  allocations for a DATA packet. Set the constant to see the messages.

- **The packet checksum for the C906** (`crc32c_riscv64.go`, `crc32c_riscv64.s`
  written by `gen_crc32c.py`). `hash/crc32` has no assembly for riscv64; its
  slicing-by-8 in Go costs 12.2 ns for a byte on the C906. The fork's loop is
  the same slicing by 8 with one 64-bit load for eight bytes, `th.extu` to cut a
  byte out and `th.lrwu` to load the table entry (XTheadBb, XTheadMemIdx, as
  WORDs: Go's assembler does not know them): 3.9 ns for a byte, measured on the
  device by `TestCRC32cMatchesHashCRC32`, which also compares it with
  `hash/crc32` for every length and alignment. The RustDesk client's SCTP has no
  zero-checksum extension (RFC 9653), so the checksum cannot be left out. Other
  architectures use `hash/crc32` (`crc32c_other.go`).

Checked with the upstream tests of v1.12.1 put back beside the fork (they are
not kept here): all pass, with the race detector too. A first version kept the
buffers for every connection; then
`TestAssociation_StreamWriteRacingCloseIsNotQueuedAfterReset` failed in 5 runs
of 12 (0 of 12 on the unchanged module), which is what the `WriteCopies` rule
is for: 0 of 12 with it.

Stand (SG2002), the RustDesk WebRTC bridge of the running server sending to a
peer with unmodified Pion, AES-128-GCM, CPU time of the server process for a
megabyte: 733 ms -> 695 ms over Wi-Fi, 738 ms -> 697 ms by wire; allocations
on the send path 43 thousand -> 28 thousand for a megabyte.
