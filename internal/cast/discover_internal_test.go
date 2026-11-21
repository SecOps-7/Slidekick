package cast

import (
	"net"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

// A typical Chromecast answer: PTR in the answers; SRV, TXT and A in the
// additional records.
func TestCollectorParsesCastAnswer(t *testing.T) {
	inst := dnsmessage.MustNewName("Chromecast-abc123._googlecast._tcp.local.")
	host := dnsmessage.MustNewName("abc123.local.")
	svc := dnsmessage.MustNewName(castService)
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	b.StartAnswers()
	b.PTRResource(dnsmessage.ResourceHeader{Name: svc, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.PTRResource{PTR: inst})
	b.StartAdditionals()
	b.SRVResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.SRVResource{Port: 8009, Target: host})
	b.TXTResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 120},
		dnsmessage.TXTResource{TXT: []string{"id=abc123", "md=Chromecast", "fn=Living room TV", "ve=05"}})
	b.AResource(dnsmessage.ResourceHeader{Name: host, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.AResource{A: [4]byte{192, 168, 1, 50}})
	pkt, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	c := newCollector()
	c.add(pkt, &net.UDPAddr{IP: net.IPv4(192, 168, 1, 50), Port: 5353})
	c.add(pkt, &net.UDPAddr{IP: net.IPv4(192, 168, 1, 50), Port: 5353}) // repeated answer
	got := c.result()
	want := Found{ID: "abc123", Name: "Living room TV", Model: "Chromecast", Host: "192.168.1.50", Port: 8009}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v", got)
	}
	if q, err := mdnsQuery(); err != nil || len(q) < 12 {
		t.Fatalf("query %v %v", q, err)
	}
}
