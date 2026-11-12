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
  written them before it gathers again. This relies on the connection not
  keeping what `Write` was given (the `io.Writer` rule); the DTLS connection
  under a data channel does not.
- **At most 16 new DATA packets for one `gatherOutbound`** (`maxGatherPackets`
  in `popPendingDataChunksToSend`), the rest at once with the next one.
  Upstream takes all the congestion window allows, hundreds of packets when
  the sender is the bottleneck, and marshals them all before the first is
  written, under the association lock.
- **One copy of a message and one array of its chunks** in `Stream.packetize`
  instead of one allocation of each for every fragment. They stay until the
  last fragment of the message is acknowledged.
- **`Tracef` calls are compiled out** (`traceLog` in `association.go`). There
  are 45 on the path of every packet sent and acknowledged, and each allocates
  for its arguments before the logger can drop the message: about ten
  allocations for a DATA packet. Set the constant to see the messages.

Checked with the upstream tests of v1.12.1 put back beside the fork (they are
not kept here): all pass. With the race detector they pass when the kept
buffers are switched off (`maxKeptPackets = 0`); with them on, the detector
reports the in-memory connection of those tests (`dumbConn2`), which keeps the
slices it is given and reads them later.

Stand (SG2002), the RustDesk WebRTC bridge of the running server sending to a
peer with unmodified Pion, AES-128-GCM, CPU time of the server process for a
megabyte: 733 ms -> 695 ms over Wi-Fi, 738 ms -> 697 ms by wire; allocations
on the send path 43 thousand -> 28 thousand for a megabyte.
