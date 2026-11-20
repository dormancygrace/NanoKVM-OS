package network

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"

	"NanoKVM-Server/config"
	"NanoKVM-Server/proto"
)

// A NanoKVM announces itself on the local network as a DNS-SD service, so
// that it can be found without knowing its name: "which NanoKVMs are here?"
// Avahi, which already answers for <hostname>.local, publishes the service
// from a file; with mDNS switched off nothing is announced. The same service
// is what GetPeers asks the network for.
const (
	discoveryType   = "_nanokvm._tcp"
	discoveryDomain = discoveryType + ".local."
)

var (
	discoveryFile = "/etc/avahi/services/nanokvm.service"
	mdnsGroup     = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
)

// discoveryService is the Avahi service file: the web interface's address
// and what a list of devices shows beside the name.
func discoveryService(conf *config.Config) []byte {
	scheme, port := "https", conf.Port.Https
	if conf.Proto != "https" {
		scheme, port = "http", conf.Port.Http
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, `<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<!-- Written by NanoKVM-Server at start; changes are replaced. -->
<service-group>
  <name replace-wildcards="yes">%%h</name>
  <service>
    <type>%s</type>
    <port>%d</port>
    <txt-record>proto=%s</txt-record>
    <txt-record>model=%s</txt-record>
  </service>
</service-group>
`, discoveryType, port, scheme, conf.Hardware.Version.String())
	return out.Bytes()
}

// PublishDiscovery writes the service file when it is not what the settings
// say. Avahi watches its directory and takes a new file by itself.
func PublishDiscovery() {
	want := discoveryService(config.GetInstance())
	if have, err := os.ReadFile(discoveryFile); err == nil && bytes.Equal(have, want) {
		// Avahi reads it as its own user, whatever the server's umask was.
		if info, err := os.Stat(discoveryFile); err == nil && info.Mode().Perm() == 0o644 {
			return
		}
	}
	temporary := discoveryFile + ".new"
	err := os.WriteFile(temporary, want, 0o644)
	if err == nil {
		err = os.Chmod(temporary, 0o644)
	}
	if err == nil {
		err = os.Rename(temporary, discoveryFile)
	}
	if err != nil {
		_ = os.Remove(temporary)
		log.Warnf("discovery: the service is not announced: %v", err)
	}
}

// Peer is a NanoKVM that announced itself.
type Peer struct {
	Name      string   `json:"name"`
	Host      string   `json:"host"`
	Addresses []string `json:"addresses"`
	Port      int      `json:"port"`
	Proto     string   `json:"proto"`
	Model     string   `json:"model"`
	// this device
	Self bool `json:"self"`
}

// GetPeers is GET /api/network/peers: the NanoKVMs that answer on the local
// network now, this one included when it announces itself.
func (s *Service) GetPeers(c *gin.Context) {
	var rsp proto.Response
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	peers := browsePeers(ctx)
	sort.Slice(peers, func(a, b int) bool { return peers[a].Name < peers[b].Name })
	c.Header("Cache-Control", "no-store")
	rsp.OkRspWithData(c, gin.H{"peers": peers})
}

// discoveryAnswers collects the records of the answers by name.
type discoveryAnswers struct {
	instances map[string]bool // the names the service type points to
	targets   map[string]dnsmessage.SRVResource
	texts     map[string][]string
	addresses map[string][]string
}

func (a *discoveryAnswers) add(message []byte) {
	var parser dnsmessage.Parser
	header, err := parser.Start(message)
	if err != nil || !header.Response {
		return
	}
	if parser.SkipAllQuestions() != nil {
		return
	}
	read := func(next func() (dnsmessage.Resource, error)) {
		for {
			resource, err := next()
			if err != nil {
				return
			}
			name := strings.ToLower(resource.Header.Name.String())
			switch body := resource.Body.(type) {
			case *dnsmessage.PTRResource:
				if name == discoveryDomain {
					a.instances[body.PTR.String()] = true
				}
			case *dnsmessage.SRVResource:
				a.targets[name] = *body
			case *dnsmessage.TXTResource:
				a.texts[name] = body.TXT
			case *dnsmessage.AResource:
				address := net.IP(body.A[:]).String()
				if !containsString(a.addresses[name], address) {
					a.addresses[name] = append(a.addresses[name], address)
				}
			}
		}
	}
	read(parser.Answer)
	if parser.SkipAllAuthorities() == nil {
		read(parser.Additional)
	}
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// discoveryQuery is one mDNS question that asks for a unicast answer, which
// a one-shot asker (not on port 5353) gets anyway.
func discoveryQuery(name string, kind dnsmessage.Type) []byte {
	parsed, err := dnsmessage.NewName(name)
	if err != nil {
		return nil
	}
	message := dnsmessage.Message{Questions: []dnsmessage.Question{{
		Name: parsed, Type: kind, Class: dnsmessage.ClassINET | 1<<15,
	}}}
	packed, err := message.Pack()
	if err != nil {
		return nil
	}
	return packed
}

// discoveryLink is the asking on one network interface. mDNS names are
// local to their link: two devices of one name on two links are two devices,
// so the records of one link are never combined with those of another.
type discoveryLink struct {
	conn    *ipv4.PacketConn
	ifIndex int
	answers *discoveryAnswers
	// local: this device's addresses on the interface.
	local []string
}

func newDiscoveryAnswers() *discoveryAnswers {
	return &discoveryAnswers{
		instances: map[string]bool{}, targets: map[string]dnsmessage.SRVResource{},
		texts: map[string][]string{}, addresses: map[string][]string{},
	}
}

// missing lists the questions for what the answers of a link leave out: the
// service record and the text records of each instance, each on its own, and
// the address of each host a service record names.
func (a *discoveryAnswers) missing() [][]byte {
	var questions [][]byte
	for instance := range a.instances {
		key := strings.ToLower(instance)
		if _, ok := a.targets[key]; !ok {
			questions = append(questions, discoveryQuery(instance, dnsmessage.TypeSRV))
		}
		if _, ok := a.texts[key]; !ok {
			questions = append(questions, discoveryQuery(instance, dnsmessage.TypeTXT))
		}
	}
	for _, target := range a.targets {
		host := target.Target.String()
		if len(a.addresses[strings.ToLower(host)]) == 0 {
			questions = append(questions, discoveryQuery(host, dnsmessage.TypeA))
		}
	}
	return questions
}

// peers are the devices whose service record this link answered.
func (a *discoveryAnswers) peers() []Peer {
	peers := []Peer{}
	for instance := range a.instances {
		key := strings.ToLower(instance)
		target, ok := a.targets[key]
		if !ok {
			continue
		}
		host := target.Target.String()
		peer := Peer{
			Name:      discoveryInstanceName(instance),
			Host:      strings.TrimSuffix(host, "."),
			Addresses: append([]string{}, a.addresses[strings.ToLower(host)]...),
			Port:      int(target.Port),
			Proto:     "https",
		}
		for _, text := range a.texts[key] {
			if value, ok := strings.CutPrefix(text, "proto="); ok && (value == "http" || value == "https") {
				peer.Proto = value
			}
			if value, ok := strings.CutPrefix(text, "model="); ok && len(value) <= 32 {
				peer.Model = value
			}
		}
		peers = append(peers, peer)
	}
	return peers
}

// browsePeers asks every network the device is on, then each network once or
// twice more for what its answers left out.
func browsePeers(ctx context.Context) []Peer {
	interfaces, err := net.Interfaces()
	if err != nil {
		return []Peer{}
	}
	var links []discoveryLink
	defer func() {
		for _, link := range links {
			link.conn.Close()
		}
	}()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		// The USB network leads to the managed computer only, which is no
		// NanoKVM and must not be able to show itself as one.
		if iface.Name == "usb0" {
			continue
		}
		var local []string
		if addresses, err := iface.Addrs(); err == nil {
			for _, address := range addresses {
				if network, ok := address.(*net.IPNet); ok && network.IP.To4() != nil {
					local = append(local, network.IP.String())
				}
			}
		}
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
		if err != nil {
			continue
		}
		link := iface
		packet := ipv4.NewPacketConn(conn)
		if err := packet.SetMulticastInterface(&link); err != nil {
			conn.Close()
			continue
		}
		if err := packet.SetControlMessage(ipv4.FlagInterface, true); err != nil {
			conn.Close()
			continue
		}
		links = append(links, discoveryLink{conn: packet, ifIndex: iface.Index, answers: newDiscoveryAnswers(), local: local})
	}
	ask := func(link discoveryLink, questions [][]byte) {
		for _, question := range questions {
			if question != nil {
				_, _ = link.conn.WriteTo(question, nil, mdnsGroup)
			}
		}
	}
	listen := func(wait time.Duration) {
		deadline := time.Now().Add(wait)
		if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
			deadline = limit
		}
		type received struct {
			link    int
			message []byte
		}
		done := make(chan received, 64)
		for i, link := range links {
			_ = link.conn.SetReadDeadline(deadline)
			go func(i int, link discoveryLink) {
				buffer := make([]byte, 9000)
				for {
					n, control, _, err := link.conn.ReadFrom(buffer)
					if err != nil {
						done <- received{link: i}
						return
					}
					if control == nil || control.IfIndex != link.ifIndex {
						continue
					}
					done <- received{link: i, message: append([]byte(nil), buffer[:n]...)}
				}
			}(i, link)
		}
		for open := len(links); open > 0; {
			got := <-done
			if got.message == nil {
				open--
				continue
			}
			links[got.link].answers.add(got.message)
		}
	}
	for _, link := range links {
		ask(link, [][]byte{discoveryQuery(discoveryDomain, dnsmessage.TypePTR)})
	}
	listen(1200 * time.Millisecond)
	// Twice at most: the second round asks for the addresses of services
	// that were only told in the first.
	for _, wait := range []time.Duration{800 * time.Millisecond, 600 * time.Millisecond} {
		asked := false
		for _, link := range links {
			if questions := link.answers.missing(); len(questions) > 0 {
				ask(link, questions)
				asked = true
			}
		}
		if !asked || ctx.Err() != nil {
			break
		}
		listen(wait)
	}

	results := make([]linkPeers, 0, len(links))
	for _, link := range links {
		results = append(results, linkPeers{peers: link.answers.peers(), local: link.local})
	}
	return collectPeers(results)
}

// linkPeers are the devices one link answered for, and this device's
// addresses on that link.
type linkPeers struct {
	peers []Peer
	local []string
}

// collectPeers lists the devices of every link, one row per link: the
// records of two links are never combined, as two devices on two links may
// announce the same names. A row is left out only when an earlier one is the
// same in everything, addresses included, which opens the same page. A row is
// this device when one of its addresses is this device's own on the link that
// answered; a row without an address is not.
func collectPeers(links []linkPeers) []Peer {
	peers := []Peer{}
	for _, link := range links {
		for _, peer := range link.peers {
			for _, address := range peer.Addresses {
				if containsString(link.local, address) {
					peer.Self = true
					break
				}
			}
			duplicate := false
			for _, p := range peers {
				if samePeer(p, peer) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				peers = append(peers, peer)
			}
		}
	}
	return peers
}

func samePeer(a, b Peer) bool {
	if a.Name != b.Name || a.Host != b.Host || a.Port != b.Port || a.Proto != b.Proto ||
		a.Model != b.Model || a.Self != b.Self || len(a.Addresses) != len(b.Addresses) {
		return false
	}
	for i := range a.Addresses {
		if a.Addresses[i] != b.Addresses[i] {
			return false
		}
	}
	return true
}

// discoveryInstanceName is the first label of an instance name, as people
// read it: "kvm\032one._nanokvm._tcp.local." is "kvm one".
func discoveryInstanceName(instance string) string {
	label := strings.TrimSuffix(instance, "."+discoveryDomain)
	var out strings.Builder
	for i := 0; i < len(label); i++ {
		if label[i] != '\\' || i+1 >= len(label) {
			out.WriteByte(label[i])
			continue
		}
		if i+3 < len(label) && isDigits(label[i+1:i+4]) {
			out.WriteByte(byte((int(label[i+1]-'0')*10+int(label[i+2]-'0'))*10 + int(label[i+3]-'0')))
			i += 3
			continue
		}
		out.WriteByte(label[i+1])
		i++
	}
	return out.String()
}

func isDigits(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}
