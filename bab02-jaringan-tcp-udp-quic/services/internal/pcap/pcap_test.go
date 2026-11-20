package pcap

import "testing"

const clientOut = `1700000000.000100 IP 10.202.2.10.40000 > 10.202.3.10.9000: Flags [S], seq 1000, win 64240, length 0
	0x0000:  4500 003c 0001 4000 4006 0000 ac1d ca0a
	0x0010:  ac1d cb0a 9c40 2328 0000 03e8 0000 0000
1700000000.100300 IP 10.202.3.10.9000 > 10.202.2.10.40000: Flags [S.], seq 5000, ack 1001, win 65160, length 0
	0x0000:  4500 003c 0000 4000 3e06 0000 ac1d cb0a
	0x0010:  ac1d ca0a 2328 9c40 0000 1388 0000 03e9
1700000000.100400 IP 10.202.2.10.40000 > 10.202.3.10.9000: Flags [P.], seq 1001:1008, ack 5001, win 502, length 7
	0x0000:  4500 0033 0002 4000 4006 0000 ac1d ca0a
	0x0010:  ac1d cb0a 9c40 2328 0000 03e9 0000 1389
1700000000.400400 IP 10.202.2.10.40000 > 10.202.3.10.9000: Flags [P.], seq 1001:1008, ack 5001, win 502, length 7
	0x0000:  4500 0033 0003 4000 4006 0000 ac1d ca0a
	0x0010:  ac1d cb0a 9c40 2328 0000 03e9 0000 1389 aa
`

const serverOut = `1700000000.050100 IP 10.202.2.10.40000 > 10.202.3.10.9000: Flags [S], seq 1000, win 64240, length 0
	0x0000:  4500 003c 0001 4000 3f06 0000 ac1d ca0a
	0x0010:  ac1d cb0a 9c40 2328 0000 03e8 0000 0000
1700000000.050300 IP 10.202.3.10.9000 > 10.202.2.10.40000: Flags [S.], seq 5000, ack 1001, win 65160, length 0
	0x0000:  4500 003c 0000 4000 4006 0000 ac1d cb0a
	0x0010:  ac1d ca0a 2328 9c40 0000 1388 0000 03e9
1700000000.450400 IP 10.202.2.10.40000 > 10.202.3.10.9000: Flags [P.], seq 1001:1008, ack 5001, win 502, length 7
	0x0000:  4500 0033 0003 4000 3f06 0000 ac1d ca0a
	0x0010:  ac1d cb0a 9c40 2328 0000 03e9 0000 1389 aa
`

func TestParseAndMatch(t *testing.T) {
	c := Parse(clientOut, 9002)
	s := Parse(serverOut, 9002)
	if len(c) != 4 || len(s) != 3 {
		t.Fatalf("jumlah packet %d/%d", len(c), len(s))
	}
	if c[0].Kind != "SYN" || c[1].Kind != "SYN-ACK" || c[2].Kind != "DATA" || c[2].Seq != "1001:1008" {
		t.Fatalf("urai salah: %+v", c[:3])
	}
	f := Match(c, s, 9000)
	if len(f) != 4 {
		t.Fatalf("flow %d", len(f))
	}
	// packet data pertama dibuang di jalan, yang kedua adalah retransmission
	var lost, retrans int
	for _, x := range f {
		if x.Kind == "DATA" && x.RecvTS == nil {
			lost++
		}
		if x.Retrans {
			retrans++
		}
	}
	if lost != 1 || retrans != 1 {
		t.Fatalf("lost=%d retrans=%d, flows=%+v", lost, retrans, f)
	}
	if f[0].Dir != "c2s" || f[1].Dir != "s2c" {
		t.Fatalf("arah salah: %s %s", f[0].Dir, f[1].Dir)
	}
}

func TestQuicKind(t *testing.T) {
	cases := map[byte]string{0xc3: "QUIC Initial", 0xe0: "QUIC Handshake", 0x41: "QUIC 1-RTT", 0xd0: "QUIC 0-RTT"}
	for b, want := range cases {
		if got := quicKind(b); got != want {
			t.Errorf("%x: %s, mau %s", b, got, want)
		}
	}
}
