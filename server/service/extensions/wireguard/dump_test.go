package wireguard

import "testing"

func TestParseDumpTakesNewestHandshakeAndSumsTransfer(t *testing.T) {
	dump := "cHJpdmF0ZQ==\tcHVibGlj\t51820\toff\n" +
		"cGVlcjE=\t(none)\t203.0.113.7:13231\t0.0.0.0/0,::/0\t1791386410\t18308\t67500\t25\n" +
		"cGVlcjI=\t(none)\t(none)\t10.7.0.2/32\t1791386500\t2\t3\toff\n" +
		"cGVlcjM=\t(none)\t(none)\t(none)\t0\t0\t0\toff\n"
	handshake, received, sent := parseDump(dump)
	if handshake != 1791386500 || received != 18310 || sent != 67503 {
		t.Fatalf("handshake %d, received %d, sent %d", handshake, received, sent)
	}
	if handshake, received, sent = parseDump("cHJpdmF0ZQ==\tcHVibGlj\t51820\toff\n"); handshake != 0 || received != 0 || sent != 0 {
		t.Fatal("a tunnel without peers reported traffic")
	}
}
