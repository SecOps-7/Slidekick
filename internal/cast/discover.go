package cast

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Found is a Cast device that answered an mDNS query.
type Found struct {
	ID    string `json:"id"`    // TXT id: the device's stable identifier
	Name  string `json:"name"`  // TXT fn: the friendly name ("Living room TV")
	Model string `json:"model"` // TXT md: e.g. "Chromecast", "Google Nest Hub"
	Host  string `json:"host"`  // an IPv4 address, else the SRV target
	Port  int    `json:"port"`
}

const castService = "_googlecast._tcp.local."

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// Discover asks the LAN for Cast devices with an mDNS query and collects
// answers until the timeout (default 2 s, at most 10 s).
//
// The query goes out from an ephemeral port, so devices answer it straight
// back to that port (RFC 6762 6.7, "legacy unicast"): nothing needs to bind
// 5353 or join the multicast group. Multicast still has to reach the LAN,
// though, which it doesn't from a Docker bridge network: there Discover
// finds nothing (and says so with an empty list, not an error), and devices
// are added by IP instead.
func Discover(ctx context.Context, timeout time.Duration) ([]Found, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	timeout = min(timeout, 10*time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	q, err := mdnsQuery()
	if err != nil {
		return nil, err
	}
	if _, err := conn.WriteToUDP(q, mdnsGroup); err != nil {
		// No route for multicast (a locked-down container): nothing to find.
		return []Found{}, nil
	}
	go func() {
		// Ask again once: mDNS is lossy and devices may drop the first query.
		select {
		case <-ctx.Done():
		case <-time.After(timeout / 3):
			conn.WriteToUDP(q, mdnsGroup)
		}
	}()
	deadline, _ := ctx.Deadline()
	conn.SetReadDeadline(deadline)

	acc := newCollector()
	buf := make([]byte, 9000)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				break
			}
			return acc.result(), nil
		}
		acc.add(buf[:n], from)
	}
	return acc.result(), nil
}

func mdnsQuery() ([]byte, error) {
	name, err := dnsmessage.NewName(castService)
	if err != nil {
		return nil, err
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	// The top bit of the class asks for a unicast reply (the "QU" bit).
	if err := b.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET | 1<<15}); err != nil {
		return nil, err
	}
	return b.Finish()
}

// collector joins PTR, SRV, TXT and A records, which may arrive in any
// order and across several packets, into devices.
type collector struct {
	instances map[string]bool                   // service instance names (from PTR)
	srv       map[string]dnsmessage.SRVResource // by instance
	txt       map[string]map[string]string      // by instance
	addr      map[string]string                 // host name -> IPv4
	from      map[string]string                 // instance -> address the answer came from
}

func newCollector() *collector {
	return &collector{instances: map[string]bool{}, srv: map[string]dnsmessage.SRVResource{}, txt: map[string]map[string]string{}, addr: map[string]string{}, from: map[string]string{}}
}

func (c *collector) add(pkt []byte, from *net.UDPAddr) {
	var p dnsmessage.Parser
	h, err := p.Start(pkt)
	if err != nil || !h.Response {
		return
	}
	if err := p.SkipAllQuestions(); err != nil {
		return
	}
	var rrs []dnsmessage.Resource
	for _, part := range []func() ([]dnsmessage.Resource, error){p.AllAnswers, p.AllAuthorities, p.AllAdditionals} {
		r, err := part()
		if err != nil {
			break
		}
		rrs = append(rrs, r...)
	}
	for _, r := range rrs {
		name := strings.ToLower(r.Header.Name.String())
		switch b := r.Body.(type) {
		case *dnsmessage.PTRResource:
			if name == castService {
				inst := strings.ToLower(b.PTR.String())
				c.instances[inst] = true
				if from != nil {
					c.from[inst] = from.IP.String()
				}
			}
		case *dnsmessage.SRVResource:
			c.srv[name] = *b
		case *dnsmessage.TXTResource:
			kv := map[string]string{}
			for _, t := range b.TXT {
				if k, v, ok := strings.Cut(t, "="); ok {
					kv[strings.ToLower(k)] = v
				}
			}
			c.txt[name] = kv
		case *dnsmessage.AResource:
			c.addr[name] = net.IP(b.A[:]).String()
		}
	}
}

func (c *collector) result() []Found {
	out := []Found{}
	seen := map[string]bool{}
	for inst := range c.instances {
		txt := c.txt[inst]
		f := Found{ID: txt["id"], Name: txt["fn"], Model: txt["md"], Port: DefaultPort}
		if s, ok := c.srv[inst]; ok {
			f.Port = int(s.Port)
			target := strings.ToLower(s.Target.String())
			f.Host = c.addr[target]
			if f.Host == "" {
				f.Host = strings.TrimSuffix(target, ".")
			}
		}
		if f.Host == "" {
			f.Host = c.from[inst]
		}
		if f.Host == "" {
			continue
		}
		if f.Name == "" {
			f.Name = strings.TrimSuffix(strings.TrimSuffix(inst, "."+castService), ".")
		}
		key := f.ID
		if key == "" {
			key = f.Host
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
